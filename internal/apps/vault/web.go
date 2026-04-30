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
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

var pageTmpl = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

// pageData is the render struct for vault HTML pages.
type pageData struct {
	TenantSlug   string
	EntryID      string
	TargetUserID string
	StaticBase   string
	APIBase      string
	Title        string
}

// ===== headers =====

// applySecurityHeaders sets the strict CSP + cross-origin headers documented
// in the plan. Same set on every vault page.
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
			"base-uri 'none'; "+
			"require-trusted-types-for 'script'")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Embedder-Policy", "require-corp")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Permissions-Policy", "clipboard-write=(self), clipboard-read=()")
}

// ===== HTML pages =====

func (a *App) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	renderPage(w, "register.html", pageData{
		TenantSlug: tenant.Slug,
		StaticBase: fmt.Sprintf("/%s/apps/vault/static", tenant.Slug),
		APIBase:    fmt.Sprintf("/%s/apps/vault/api", tenant.Slug),
		Title:      "Set up vault",
	})
}

func (a *App) handleAddPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	renderPage(w, "add.html", pageData{
		TenantSlug: tenant.Slug,
		StaticBase: fmt.Sprintf("/%s/apps/vault/static", tenant.Slug),
		APIBase:    fmt.Sprintf("/%s/apps/vault/api", tenant.Slug),
		Title:      "Add a secret",
	})
}

func (a *App) handleRevealPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	entryID := r.PathValue("entry_id")
	if _, err := uuid.Parse(entryID); err != nil {
		http.Error(w, "bad entry id", http.StatusBadRequest)
		return
	}
	renderPage(w, "reveal.html", pageData{
		TenantSlug: tenant.Slug,
		EntryID:    entryID,
		StaticBase: fmt.Sprintf("/%s/apps/vault/static", tenant.Slug),
		APIBase:    fmt.Sprintf("/%s/apps/vault/api", tenant.Slug),
		Title:      "Reveal secret",
	})
}

func (a *App) handleGrantPage(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	targetID := r.PathValue("user_id")
	if _, err := uuid.Parse(targetID); err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}
	renderPage(w, "grant.html", pageData{
		TenantSlug:   tenant.Slug,
		TargetUserID: targetID,
		StaticBase:   fmt.Sprintf("/%s/apps/vault/static", tenant.Slug),
		APIBase:      fmt.Sprintf("/%s/apps/vault/api", tenant.Slug),
		Title:        "Grant vault access",
	})
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

func (a *App) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		AuthHash                 []byte          `json:"auth_hash"`
		KDFParams                json.RawMessage `json:"kdf_params"`
		UserPublicKey            []byte          `json:"user_public_key"`
		UserPrivateKeyCiphertext []byte          `json:"user_private_key_ciphertext"`
		UserPrivateKeyNonce      []byte          `json:"user_private_key_nonce"`
		WrappedVaultKey          []byte          `json:"wrapped_vault_key,omitempty"`
		Replace                  bool            `json:"replace,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	err := a.svc.Register(r.Context(), caller, RegisterParams{
		AuthHash:                 body.AuthHash,
		KDFParams:                body.KDFParams,
		UserPublicKey:            body.UserPublicKey,
		UserPrivateKeyCiphertext: body.UserPrivateKeyCiphertext,
		UserPrivateKeyNonce:      body.UserPrivateKeyNonce,
		WrappedVaultKey:          body.WrappedVaultKey,
		Replace:                  body.Replace,
	}, audit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleSelfUnlockTest(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		AuthHash []byte `json:"auth_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := a.svc.SelfUnlockTest(r.Context(), caller, body.AuthHash); err != nil {
		http.Error(w, "verification failed", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		case errors.Is(err, ErrUnlockNotGranted):
			http.Error(w, "no access yet — ask an admin", http.StatusForbidden)
		case errors.Is(err, ErrUnlockPending):
			http.Error(w, "registration pending", http.StatusConflict)
		default:
			slog.Error("vault: unlock", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *App) handleLock(w http.ResponseWriter, r *http.Request) {
	// Pure client-side lock for v1: the browser wipes IndexedDB and
	// terminates the SharedWorker. Server has no per-session state to
	// clear beyond the session cookie itself, which we leave intact
	// (logging the user out of Kit is a separate flow).
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe returns the caller's vault_users metadata: kdf_params (so the
// browser can derive auth_hash on a fresh device) plus state flags. Returns
// 404 if the caller has no row yet (browser sends to /register).
func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	v, err := models.GetVaultUser(r.Context(), a.pool, caller.TenantID, caller.UserID)
	if err != nil {
		slog.Error("vault: GetVaultUser", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if v == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kdf_params": v.KDFParams,
		"pending":    v.Pending,
		"granted":    v.WrappedVaultKey != nil,
	})
}

// handleGetUser returns a teammate's pubkey + fingerprint for the grant page.
// Caller must already have vault access (so they can wrap vault_key for the
// target). Returns 404 if the target hasn't registered yet.
func (a *App) handleGetUser(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	targetID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Authz: caller must themselves be a granted vault member.
	self, err := models.GetVaultUser(r.Context(), a.pool, caller.TenantID, caller.UserID)
	if err != nil || self == nil || self.WrappedVaultKey == nil {
		http.Error(w, "vault access required", http.StatusForbidden)
		return
	}
	target, err := models.GetVaultUser(r.Context(), a.pool, caller.TenantID, targetID)
	if err != nil {
		slog.Error("vault: GetVaultUser target", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"public_key":  target.UserPublicKey,
		"fingerprint": pubkeyFingerprint(target.UserPublicKey),
		"pending":     target.Pending,
		"granted":     target.WrappedVaultKey != nil,
		"reset":       target.ResetPendingUntil != nil,
	})
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
	rows, err := a.svc.ListEntries(r.Context(), caller, q, tag, limit)
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
		Title           string                   `json:"title"`
		Username        string                   `json:"username,omitempty"`
		URL             string                   `json:"url,omitempty"`
		Tags            []string                 `json:"tags,omitempty"`
		ValueCiphertext []byte                   `json:"value_ciphertext"`
		ValueNonce      []byte                   `json:"value_nonce"`
		Scopes          []models.VaultEntryScope `json:"scopes,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
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
		Scopes:          body.Scopes,
	}, audit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *App) handleGetEntry(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	entryID, err := uuid.Parse(r.PathValue("entry_id"))
	if err != nil {
		http.NotFound(w, r) // uniform 404 for bad/no-scope/missing IDs
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
		Title           string                   `json:"title"`
		Username        string                   `json:"username,omitempty"`
		URL             string                   `json:"url,omitempty"`
		Tags            []string                 `json:"tags,omitempty"`
		ValueCiphertext []byte                   `json:"value_ciphertext"`
		ValueNonce      []byte                   `json:"value_nonce"`
		Scopes          []models.VaultEntryScope `json:"scopes,omitempty"`
		UpdateScopes    bool                     `json:"update_scopes,omitempty"`
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
		Scopes:          body.Scopes,
		UpdateScopes:    body.UpdateScopes,
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

// ===== grants =====

func (a *App) handleGrant(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	targetID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}
	var body struct {
		WrappedVaultKey []byte `json:"wrapped_vault_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	err = a.svc.Grant(r.Context(), caller, GrantParams{
		TargetUserID:    targetID,
		WrappedVaultKey: body.WrappedVaultKey,
	}, audit)
	if err != nil {
		switch {
		case errors.Is(err, models.ErrVaultUserNotFound):
			http.NotFound(w, r)
		case errors.Is(err, ErrStepUpRequired):
			http.Error(w, "recent unlock required", http.StatusUnauthorized)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if !caller.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	targetID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}
	audit := a.svc.AuditFromRequest(caller, r)
	if err := a.svc.RevokeGrant(r.Context(), caller, targetID, audit); err != nil {
		slog.Error("vault: revoke grant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeclinePending drops a vault_users row that hasn't been granted
// yet. The "Decline" button on the admin's grant decision card calls
// this; rejecting a registration is recoverable (the user can re-register).
// Refuses to delete a row that already has a wrapped_vault_key — those
// require RevokeGrant, which is a separate, more deliberate action.
func (a *App) handleDeclinePending(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if !caller.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	targetID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}
	target, err := models.GetVaultUser(r.Context(), a.pool, caller.TenantID, targetID)
	if err != nil {
		slog.Error("vault: decline lookup", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.NotFound(w, r)
		return
	}
	if target.WrappedVaultKey != nil {
		http.Error(w, "user already has access; use revoke instead", http.StatusConflict)
		return
	}
	if _, err := a.pool.Exec(r.Context(),
		`DELETE FROM app_vault_users WHERE tenant_id = $1 AND user_id = $2 AND wrapped_vault_key IS NULL`,
		caller.TenantID, targetID,
	); err != nil {
		slog.Error("vault: decline delete", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.svc.AuditFromRequest(caller, r).log(r.Context(), "vault.revoke_grant", "vault_user", &targetID, EvtRevokeGrant{})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ===== misc =====

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
