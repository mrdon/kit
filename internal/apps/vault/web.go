package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// ===== reveal deep-link bridge =====

// handleRevealPage is the terminal handler on the reveal route. By the
// time it runs, the deep-link middleware has already consumed any one-shot
// token and minted a /{slug}/ session cookie (or the request arrived with
// an existing cookie). Its only job is to bounce the now-authenticated
// browser into the React vault entry, which lives under the same cookie
// path. There is no vault HTML left to render here — this route exists
// purely to turn a Slack one-shot token into a console session.
func (a *App) handleRevealPage(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, fmt.Sprintf("/%s/web/vault/%s", tenant.Slug, entryID), http.StatusSeeOther)
}

// handleDeepLinkSuccess is the OnSuccess callback wired into the
// deep-link middleware. Writes a verified-tenant audit row recording
// the consumed jti so we can correlate to the agent's view_secret
// action in incident review.
func (a *App) handleDeepLinkSuccess(r *http.Request, claims *auth.Claims) {
	if a.svc == nil || claims == nil {
		return
	}
	actor := claims.UserID
	actx := auditCtx{
		pool:      a.svc.pool,
		tenantID:  claims.TenantID,
		actorID:   &actor,
		ip:        clientIP(r),
		userAgent: clientUA(r),
	}
	actx.log(r.Context(), "vault.token_consumed", "vault_entry", &claims.EntryID, EvtTokenConsumed{
		EntryID: claims.EntryID,
		JTI:     claims.JTI,
	})
}

// handleDeepLinkError is the OnError callback wired into the deep-link
// middleware. For verified-tenant failures (claims != nil) it writes an
// audit row scoped to the token's tenant; for bad_sig / malformed it
// only slog.Warns since the claimed tenant is untrusted. In every case
// it renders a friendly error page that guides the user to ask Kit for
// a fresh link or open the vault manually — explicitly NOT a redirect
// to /login (per plan, the user isn't logged out, the link is stale).
func (a *App) handleDeepLinkError(w http.ResponseWriter, r *http.Request, reason auth.DeepLinkReason, claims *auth.Claims) {
	switch reason {
	case auth.DeepLinkBadSig, auth.DeepLinkMalformed:
		slog.Warn("vault: deep-link rejected (untrusted payload)", "reason", reason)
	case auth.DeepLinkExpired, auth.DeepLinkConsumed, auth.DeepLinkTenantMismatch, auth.DeepLinkEntryMismatch:
		if claims != nil && a.svc != nil {
			actor := claims.UserID
			actx := auditCtx{
				pool:      a.svc.pool,
				tenantID:  claims.TenantID,
				actorID:   &actor,
				ip:        clientIP(r),
				userAgent: clientUA(r),
			}
			actx.log(r.Context(), "vault.token_rejected", "vault_entry", &claims.EntryID, EvtTokenRejected{
				Reason:  string(reason),
				EntryID: claims.EntryID,
				JTI:     claims.JTI,
			})
		} else {
			slog.Warn("vault: deep-link rejected without claims", "reason", reason)
		}
	}

	// The token was consumed (or never valid); there's nothing to render
	// here anymore. Bounce the user into the React vault with a reason
	// code so the page can explain why their tap didn't open the secret.
	// Errors that never resolved a tenant fall back to a bare 403 since we
	// have no slug to redirect under.
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "deep-link rejected", http.StatusForbidden)
		return
	}
	dest := fmt.Sprintf("/%s/web/vault?reveal_error=%s", tenant.Slug, deepLinkErrorCode(reason))
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// deepLinkErrorCode maps a structured reason to the short code carried on
// the React vault's ?reveal_error query. Kept narrow on purpose — the
// human-facing copy lives in the console (revealErrorCopy) and every
// variant resolves to "ask for a fresh link" without leaking verification
// mechanics that could help an attacker probe.
func deepLinkErrorCode(reason auth.DeepLinkReason) string {
	switch reason {
	case auth.DeepLinkExpired:
		return "expired"
	case auth.DeepLinkConsumed:
		return "consumed"
	case auth.DeepLinkEntryMismatch:
		return "entry_mismatch"
	case auth.DeepLinkTenantMismatch:
		return "tenant_mismatch"
	case auth.DeepLinkBadSig, auth.DeepLinkMalformed:
		return "invalid"
	default:
		return "invalid"
	}
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
			// Wrong master password. This is NOT a web-session failure:
			// the handler only runs behind requireCaller, so the caller is
			// authenticated. Return 403 (never 401) so the client shows
			// "incorrect password" inline instead of bouncing a logged-in
			// user to the login page. The console vaultApi maps 401 — and
			// only 401 — to a login redirect.
			slog.Info("vault: unlock rejected — bad password",
				"tenant_id", caller.TenantID, "user_id", caller.UserID)
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "Incorrect vault password.",
			})
		case errors.Is(err, ErrUnlockLocked):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "Too many unlock attempts. Wait a moment and try again.",
			})
		case errors.Is(err, ErrUnlockNotSetUp):
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "This workspace has no vault yet. Ask an admin to set one up.",
			})
		default:
			slog.Error("vault: unlock", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Internal error unlocking the vault.",
			})
		}
		return
	}
	slog.Info("vault: unlock ok", "tenant_id", caller.TenantID, "user_id", caller.UserID)
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

// handlePrincipals lists the roles the caller can scope a secret to,
// plus the tenant's default_role_id (the 'member' role that includes
// everyone). The add/reveal pages use this for the "who can see this"
// selector. Non-admins see only roles they're a member of; admins see
// every role in the tenant, mirroring validateRoleForCaller's admin
// exemption so the picker can actually offer those choices.
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
		if !caller.IsAdmin && !memberOf[role.ID] {
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
