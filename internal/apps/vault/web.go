package vault

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/web/chrome"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// pageTmpl is the vault template set with the shared chrome header
// partial mixed in. We clone chrome.Tmpl() (don't ParseFS into it
// directly — Clone is the documented way to extend a parsed template
// with another file set) so the {{ template "chrome_header" . }} call
// inside each vault template resolves.
var pageTmpl = template.Must(chrome.Tmpl().ParseFS(templatesFS, "templates/*.html"))

// pageData is the render struct for vault HTML pages.
type pageData struct {
	TenantSlug string
	EntryID    string
	StaticBase string
	APIBase    string
	ChromeCSS  string
	Title      string
	Header     chrome.Header
	// EntryCount is rendered on /nuke so the admin can see exactly how
	// many secrets they're about to destroy.
	EntryCount int
}

// basePageData returns common page-render fields populated from the
// request context, including the shared chrome header.
func (a *App) basePageData(r *http.Request) pageData {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		return pageData{}
	}
	homeURL := fmt.Sprintf("/%s/apps/vault/list", tenant.Slug)
	return pageData{
		TenantSlug: tenant.Slug,
		StaticBase: fmt.Sprintf("/%s/apps/vault/static", tenant.Slug),
		APIBase:    fmt.Sprintf("/%s/apps/vault/api", tenant.Slug),
		ChromeCSS:  chrome.HeaderCSSPath,
		Header:     chrome.For(r, a.pool, homeURL),
	}
}

// ===== headers =====

// applySecurityHeaders sets the strict CSP + cross-origin headers on
// every vault page.
func applySecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy",
		"default-src 'none'; "+
			"script-src 'self'; "+
			"style-src 'self'; "+
			"connect-src 'self'; "+
			"img-src 'self' data:; "+
			"worker-src 'self'; "+
			"form-action 'self'; "+
			"frame-ancestors 'none'; "+
			"base-uri 'none'")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Embedder-Policy", "require-corp")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Permissions-Policy", "clipboard-write=(self), clipboard-read=()")
}

// ===== HTML pages =====

func (a *App) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || !caller.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	pd := a.basePageData(r)
	if pd.TenantSlug == "" {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	pd.Title = "Set up vault"
	renderPage(w, "setup.html", pd)
}

func (a *App) handleRotatePage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || !caller.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	pd := a.basePageData(r)
	if pd.TenantSlug == "" {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	pd.Title = "Rotate vault password"
	renderPage(w, "rotate.html", pd)
}

func (a *App) handleNukePage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || !caller.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	pd := a.basePageData(r)
	if pd.TenantSlug == "" {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	// Count entries so the warning screen can show the real number.
	if err := a.pool.QueryRow(r.Context(),
		`SELECT count(*) FROM app_vault_entries WHERE tenant_id = $1`,
		caller.TenantID,
	).Scan(&pd.EntryCount); err != nil {
		slog.Error("vault: counting entries for nuke page", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pd.Title = "Destroy vault"
	renderPage(w, "nuke.html", pd)
}

func (a *App) handleListPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	pd := a.basePageData(r)
	if pd.TenantSlug == "" {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	pd.Title = "Your secrets"
	renderPage(w, "list.html", pd)
}

func (a *App) handleAddPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	pd := a.basePageData(r)
	if pd.TenantSlug == "" {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	pd.Title = "Add a secret"
	renderPage(w, "add.html", pd)
}

func (a *App) handleRevealPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	pd := a.basePageData(r)
	if pd.TenantSlug == "" {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	entryID := r.PathValue("entry_id")
	if _, err := uuid.Parse(entryID); err != nil {
		http.Error(w, "bad entry id", http.StatusBadRequest)
		return
	}
	pd.EntryID = entryID
	pd.Title = "Reveal secret"
	renderPage(w, "reveal.html", pd)
}

func renderPage(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("vault: rendering template", "name", name, "error", err)
	}
}

// ===== static =====

func (a *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	prefix := fmt.Sprintf("/%s/apps/vault/static/", tenant.Slug)
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	if rel == "" || strings.Contains(rel, "..") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, err := fs.ReadFile(staticFS, "static/"+rel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if mt := mime.TypeByExtension(filepath.Ext(rel)); mt != "" {
		w.Header().Set("Content-Type", mt)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// ===== JSON API =====

// handleSetupPost initializes the tenant vault. Admin-only; refuses if
// the tenant already has a row. The browser has already derived all the
// crypto from the master password the admin typed; the server just
// validates shapes and persists.
func (a *App) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		KDFParams            json.RawMessage `json:"kdf_params"`
		AuthHash             []byte          `json:"auth_hash"`
		WrappedVaultKey      []byte          `json:"wrapped_vault_key"`
		WrappedVaultKeyNonce []byte          `json:"wrapped_vault_key_nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	err := a.svc.SetupVault(r.Context(), caller, SetupParams{
		KDFParams:            body.KDFParams,
		AuthHash:             body.AuthHash,
		WrappedVaultKey:      body.WrappedVaultKey,
		WrappedVaultKeyNonce: body.WrappedVaultKeyNonce,
	}, a.svc.AuditFromRequest(caller, r))
	if err != nil {
		switch {
		case errors.Is(err, services.ErrForbidden):
			http.Error(w, "admin only", http.StatusForbidden)
		case errors.Is(err, models.ErrVaultAlreadySetup):
			http.Error(w, "vault already set up; use /rotate or /nuke", http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleRotatePost(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		KDFParams            json.RawMessage `json:"kdf_params"`
		AuthHash             []byte          `json:"auth_hash"`
		WrappedVaultKey      []byte          `json:"wrapped_vault_key"`
		WrappedVaultKeyNonce []byte          `json:"wrapped_vault_key_nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	newGen, err := a.svc.RotateVaultPassword(r.Context(), caller, RotateParams{
		KDFParams:            body.KDFParams,
		AuthHash:             body.AuthHash,
		WrappedVaultKey:      body.WrappedVaultKey,
		WrappedVaultKeyNonce: body.WrappedVaultKeyNonce,
	}, a.svc.AuditFromRequest(caller, r))
	if err != nil {
		switch {
		case errors.Is(err, services.ErrForbidden):
			http.Error(w, "admin only", http.StatusForbidden)
		case errors.Is(err, ErrStepUpRequired):
			http.Error(w, "recent unlock required", http.StatusUnauthorized)
		case errors.Is(err, models.ErrVaultNotSetUp):
			http.Error(w, "vault not set up; use /setup", http.StatusNotFound)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vault_generation": newGen})
}

// handleNukePost destroys the vault. Admin-only. Requires the caller to
// re-type the tenant slug as a confirmation gate (defense against
// click-fatigue, accidental script invocations, and stale tabs).
func (a *App) handleNukePost(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	var body struct {
		ConfirmSlug string `json:"confirm_slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.ConfirmSlug != tenant.Slug {
		http.Error(w, "confirm_slug does not match tenant slug", http.StatusBadRequest)
		return
	}
	count, err := a.svc.NukeVault(r.Context(), caller, a.svc.AuditFromRequest(caller, r))
	if err != nil {
		switch {
		case errors.Is(err, services.ErrForbidden):
			http.Error(w, "admin only", http.StatusForbidden)
		default:
			slog.Error("vault: nuke", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries_destroyed": count})
}

func (a *App) handleUnlock(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		AuthHash []byte `json:"auth_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	res, err := a.svc.Unlock(r.Context(), caller, body.AuthHash, audit)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnlockMismatch):
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case errors.Is(err, ErrUnlockLocked):
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
		case errors.Is(err, ErrUnlockNotSetUp):
			http.Error(w, "vault not set up", http.StatusNotFound)
		default:
			slog.Error("vault: unlock", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *App) handleLock(w http.ResponseWriter, r *http.Request) {
	// Pure client-side lock: browser wipes IndexedDB and terminates the
	// SharedWorker. Server has no per-session vault state to clear.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleStatus returns whether the tenant vault is initialized + the
// kdf_params (so the browser can derive auth_hash on a fresh device).
// Plain 200 with `set_up: false` if not set up — easier for the
// page-bootstrap JS than parsing a 404. Always includes
// `tenant_id_bytes` so the browser knows what to use as the AES-GCM AAD
// when wrapping at setup time (before any tenant row exists).
func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	v, err := models.GetVaultTenant(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		slog.Error("vault: GetVaultTenant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := map[string]any{
		"set_up":          v != nil,
		"tenant_id_bytes": encodeTenantIDForAAD(caller.TenantID),
	}
	if v != nil {
		out["kdf_params"] = v.KDFParams
		out["vault_generation"] = v.VaultGeneration
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePrincipals lists the roles the caller is a member of, plus the
// tenant's default_role_id (the 'member' role that includes everyone).
// The add/reveal pages use this for the "who can see this" selector.
func (a *App) handlePrincipals(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	tenant, err := models.GetTenantByID(r.Context(), a.pool, caller.TenantID)
	if err != nil || tenant == nil {
		slog.Error("vault: loading tenant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	roles, err := models.ListRoles(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		slog.Error("vault: listing roles", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	memberOf := make(map[uuid.UUID]bool, len(caller.RoleIDs))
	for _, id := range caller.RoleIDs {
		memberOf[id] = true
	}
	type roleOut struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	roleList := make([]roleOut, 0, len(roles))
	for _, role := range roles {
		if !memberOf[role.ID] {
			continue
		}
		roleList = append(roleList, roleOut{ID: role.ID.String(), Name: role.Name})
	}
	out := map[string]any{
		"roles": roleList,
	}
	if tenant.DefaultRoleID != nil {
		out["default_role_id"] = tenant.DefaultRoleID.String()
	}
	writeJSON(w, http.StatusOK, out)
}

// ===== entry CRUD =====

func (a *App) handleListEntries(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	q := r.URL.Query().Get("q")
	tag := r.URL.Query().Get("tag")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	var roleID *uuid.UUID
	if rs := r.URL.Query().Get("role_id"); rs != "" {
		rid, err := uuid.Parse(rs)
		if err != nil {
			http.Error(w, "bad role_id", http.StatusBadRequest)
			return
		}
		roleID = &rid
	}
	rows, err := a.svc.ListEntries(r.Context(), caller, q, tag, roleID, limit)
	if err != nil {
		slog.Error("vault: list entries", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (a *App) handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		Title           string   `json:"title"`
		Username        string   `json:"username,omitempty"`
		URL             string   `json:"url,omitempty"`
		Tags            []string `json:"tags,omitempty"`
		ValueCiphertext []byte   `json:"value_ciphertext"`
		ValueNonce      []byte   `json:"value_nonce"`
		RoleID          *string  `json:"role_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	roleID, err := parseOptionalUUID(body.RoleID)
	if err != nil {
		http.Error(w, "bad role_id", http.StatusBadRequest)
		return
	}
	if roleID == nil {
		http.Error(w, "role_id required", http.StatusBadRequest)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	id, err := a.svc.CreateEntry(r.Context(), caller, CreateEntryParams{
		Title:           body.Title,
		Username:        body.Username,
		URL:             body.URL,
		Tags:            body.Tags,
		ValueCiphertext: body.ValueCiphertext,
		ValueNonce:      body.ValueNonce,
		RoleID:          roleID,
	}, audit)
	if err != nil {
		if errors.Is(err, ErrCallerNotInRole) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// parseOptionalUUID returns nil when the input pointer is nil or its
// value is an empty string; otherwise parses the UUID.
func parseOptionalUUID(s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil //nolint:nilnil // explicit "not provided" sentinel
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (a *App) handleGetEntry(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	entryID, err := uuid.Parse(r.PathValue("entry_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	e, err := a.svc.GetEntry(r.Context(), caller, entryID, audit)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("vault: get entry", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (a *App) handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	entryID, err := uuid.Parse(r.PathValue("entry_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Title           string   `json:"title"`
		Username        string   `json:"username,omitempty"`
		URL             string   `json:"url,omitempty"`
		Tags            []string `json:"tags,omitempty"`
		ValueCiphertext []byte   `json:"value_ciphertext"`
		ValueNonce      []byte   `json:"value_nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	err = a.svc.UpdateEntry(r.Context(), caller, entryID, UpdateEntryParams{
		Title:           body.Title,
		Username:        body.Username,
		URL:             body.URL,
		Tags:            body.Tags,
		ValueCiphertext: body.ValueCiphertext,
		ValueNonce:      body.ValueNonce,
	}, audit)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleSetEntryRole(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	entryID, err := uuid.Parse(r.PathValue("entry_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		RoleID *string `json:"role_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	roleID, err := parseOptionalUUID(body.RoleID)
	if err != nil {
		http.Error(w, "bad role_id", http.StatusBadRequest)
		return
	}
	if roleID == nil {
		http.Error(w, "role_id required", http.StatusBadRequest)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	if err := a.svc.SetEntryRole(r.Context(), caller, entryID, roleID, audit); err != nil {
		switch {
		case errors.Is(err, models.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, ErrStepUpRequired):
			http.Error(w, "recent unlock required", http.StatusUnauthorized)
		case errors.Is(err, ErrCallerNotInRole):
			http.Error(w, err.Error(), http.StatusForbidden)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	entryID, err := uuid.Parse(r.PathValue("entry_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	if err := a.svc.DeleteEntry(r.Context(), caller, entryID, audit); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("vault: delete entry", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ===== misc =====

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
