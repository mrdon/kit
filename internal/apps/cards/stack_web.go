package cards

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// providerTimeout caps how long a single provider can take to produce its
// page before the host gives up and marks it degraded. Keeps one slow app
// from blocking the whole stack.
const providerTimeout = 2 * time.Second

// defaultGlobalLimit is the cap on total items returned by /api/v1/stack.
const defaultGlobalLimit = 100

// registerStackRoutes wires the generic, provider-agnostic stack endpoints.
// The cards app owns the mux because it was already the PWA's host.
//
// All routes live under /{slug}/api/v1/... so the workspace-scoped
// session cookie (Path=/{slug}/) is sent with them. The middleware
// chain runs TenantFromPath first (rejecting unknown slugs with 404),
// then the session middleware, then AssertTenantMatch (which 403s if
// caller.TenantID differs from the path tenant — defense in depth
// against session-tenant-vs-URL-tenant mismatches).
func registerStackRoutes(mux *http.ServeMux, a *CardsApp) {
	if a.signer == nil {
		return
	}
	tenantMW := auth.TenantFromPath(a.pool)
	wrap := func(h http.HandlerFunc) http.Handler {
		return tenantMW(requireJSON(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h)))))
	}
	// Chat transcribe takes multipart audio, so it uses the custom
	// header CSRF wrapper instead of requireJSON.
	wrapCSRF := func(h http.HandlerFunc) http.Handler {
		return tenantMW(requireCSRFHeader(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h)))))
	}
	mux.Handle("GET /{slug}/api/v1/stack", wrap(handleStackList))
	mux.Handle("GET /{slug}/api/v1/stack/items/{source_app}/{kind}/{id}", wrap(handleStackItemDetail))
	mux.Handle("POST /{slug}/api/v1/stack/items/{source_app}/{kind}/{id}/action", wrap(handleStackItemAction))
	mux.Handle("POST /{slug}/api/v1/stack/items/{source_app}/{kind}/{id}/chat/transcribe", wrapCSRF(a.handleChatTranscribe))
	mux.Handle("POST /{slug}/api/v1/stack/items/{source_app}/{kind}/{id}/chat/execute", wrap(a.handleChatExecute))
}

// stackResponse is the wire type for GET /api/v1/stack.
type stackResponse struct {
	Items       []shared.StackItem `json:"items"`
	NextCursors map[string]string  `json:"next_cursors,omitempty"`
	Degraded    []degradedProvider `json:"degraded,omitempty"`
}

type degradedProvider struct {
	SourceApp string `json:"source_app"`
	ErrorCode string `json:"error_code"`
}

func handleStackList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	limit := parseIntDefault(r.URL.Query().Get("limit"), defaultGlobalLimit)
	cursors := decodeStackCursor(r.URL.Query().Get("cursor"))

	providers := apps.CardProviders()
	type result struct {
		sourceApp string
		page      shared.StackPage
		err       error
	}
	results := make([]result, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func(i int, p apps.CardProvider) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), providerTimeout)
			defer cancel()
			page, err := p.StackItems(ctx, caller, cursors[p.SourceApp()], limit)
			results[i] = result{sourceApp: p.SourceApp(), page: page, err: err}
		}(i, p)
	}
	wg.Wait()

	var all []shared.StackItem
	nextCursors := map[string]string{}
	var degraded []degradedProvider
	for _, rs := range results {
		if rs.err != nil {
			code := "error"
			if errors.Is(rs.err, context.DeadlineExceeded) {
				code = "timeout"
			}
			slog.Error("stack provider failed", "source_app", rs.sourceApp, "error", rs.err)
			degraded = append(degraded, degradedProvider{SourceApp: rs.sourceApp, ErrorCode: code})
			continue
		}
		all = append(all, rs.page.Items...)
		if rs.page.NextCursor != "" {
			nextCursors[rs.sourceApp] = rs.page.NextCursor
		}
	}

	sortStackItems(all)
	if len(all) > limit {
		all = all[:limit]
	}

	resp := stackResponse{Items: all, Degraded: degraded}
	if len(nextCursors) > 0 {
		resp.NextCursors = nextCursors
	}
	writeJSON(w, http.StatusOK, resp)
}

// sortStackItems applies the canonical sort: tier rank, kind weight,
// newest first, then compound key for a deterministic tiebreak.
func sortStackItems(items []shared.StackItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		ra, rb := a.PriorityTier.Rank(), b.PriorityTier.Rank()
		if ra != rb {
			return ra < rb
		}
		if a.KindWeight != b.KindWeight {
			return a.KindWeight < b.KindWeight
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return shared.Key(a.SourceApp, a.Kind, a.ID) < shared.Key(b.SourceApp, b.Kind, b.ID)
	})
}

func handleStackItemDetail(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	sourceApp := r.PathValue("source_app")
	kind := r.PathValue("kind")
	id := r.PathValue("id")
	p := providerByName(sourceApp)
	if p == nil {
		writeErr(w, http.StatusNotFound, errors.New("unknown source_app"))
		return
	}
	detail, err := p.GetItem(r.Context(), caller, kind, id)
	if err != nil {
		writeStackErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

type actionRequest struct {
	ActionID string          `json:"action_id"`
	Params   json.RawMessage `json:"params,omitempty"`
}

func handleStackItemAction(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	sourceApp := r.PathValue("source_app")
	kind := r.PathValue("kind")
	id := r.PathValue("id")
	p := providerByName(sourceApp)
	if p == nil {
		writeErr(w, http.StatusNotFound, errors.New("unknown source_app"))
		return
	}
	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	if req.ActionID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("action_id required"))
		return
	}
	result, err := p.DoAction(r.Context(), caller, kind, id, req.ActionID, req.Params)
	if err != nil {
		writeStackErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func providerByName(name string) apps.CardProvider {
	for _, p := range apps.CardProviders() {
		if p.SourceApp() == name {
			return p
		}
	}
	return nil
}

func writeStackErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, services.ErrForbidden):
		writeErr(w, http.StatusForbidden, err)
	case errors.Is(err, ErrAlreadyTerminal):
		writeErr(w, http.StatusConflict, err)
	case errors.Is(err, ErrOptionNotFound), errors.Is(err, ErrNoOptionPicked):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// decodeStackCursor parses the client's opaque cursor. Empty input or
// malformed base64/JSON yields an empty map — providers treat that as
// "first page", which is the desired behavior on fresh loads.
func decodeStackCursor(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}
