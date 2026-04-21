package integrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
)

//go:embed templates/integration_setup.html
var templatesFS embed.FS

var setupTmpl = template.Must(template.ParseFS(templatesFS, "templates/integration_setup.html"))

// formField is the render model for one input on the setup form.
type formField struct {
	Name      string
	Label     string
	InputType string
	Required  bool
	Help      string
}

// formModel is the data passed to the template.
type formModel struct {
	Title          string
	DisplayName    string
	Description    string
	Scope          string
	Action         string
	Token          string
	Fields         []formField // primary fields, always visible
	AdvancedFields []formField // advanced fields, collapsed under <details>
	Error          string
	Success        bool
	SuccessNote    string
}

func (a *App) registerRoutes(mux *http.ServeMux) {
	if a.pool == nil {
		slog.Warn("integrations: pool not set at route registration; routes skipped")
		return
	}
	tenantMW := auth.TenantFromPath(a.pool)
	mux.Handle("GET /{slug}/integrations/setup", tenantMW(http.HandlerFunc(a.handleSetupGet)))
	mux.Handle("POST /{slug}/integrations/setup", tenantMW(http.HandlerFunc(a.handleSetupPost)))
}

func (a *App) handleSetupGet(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	p, spec, err := a.verifyAndLoad(r.Context(), tenant.ID, token)
	if err != nil {
		a.renderError(w, err)
		return
	}

	primary, advanced := splitFormFields(spec)
	model := formModel{
		Title:          "Configure " + spec.DisplayName,
		DisplayName:    spec.DisplayName,
		Description:    spec.Description,
		Scope:          string(spec.Scope),
		Action:         fmt.Sprintf("/%s/integrations/setup", tenant.Slug),
		Token:          token,
		Fields:         primary,
		AdvancedFields: advanced,
	}
	if p.Status != models.PendingStatusPending {
		model.Error = "This setup link has already been used or is no longer valid."
	}
	renderForm(w, model)
}

func (a *App) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	token := r.FormValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	p, spec, err := a.verifyAndLoad(r.Context(), tenant.ID, token)
	if err != nil {
		a.renderError(w, err)
		return
	}

	formAction := fmt.Sprintf("/%s/integrations/setup", tenant.Slug)

	if p.Status != models.PendingStatusPending {
		primary, advanced := splitFormFields(spec)
		renderForm(w, formModel{
			Title:          "Configure " + spec.DisplayName,
			DisplayName:    spec.DisplayName,
			Description:    spec.Description,
			Scope:          string(spec.Scope),
			Action:         formAction,
			Token:          token,
			Fields:         primary,
			AdvancedFields: advanced,
			Error:          "This setup link has already been used.",
		})
		return
	}

	var (
		usernamePtr     *string
		primaryEncPtr   *string
		secondaryEncPtr *string
		configMap       = map[string]any{}
		validationError string
	)

	for _, f := range spec.Fields {
		value := strings.TrimSpace(r.FormValue(f.Name))
		if value == "" {
			if f.Required {
				validationError = displayFieldLabel(f) + " is required."
				break
			}
			continue
		}
		switch f.effectiveTarget() {
		case TargetConfig:
			configMap[f.Name] = value
		case TargetUsername:
			v := value
			usernamePtr = &v
		case TargetPrimaryToken:
			enc, encErr := a.encryptSecret(value)
			if encErr != nil {
				validationError = encErr.Error()
				break
			}
			primaryEncPtr = &enc
		case TargetSecondaryToken:
			enc, encErr := a.encryptSecret(value)
			if encErr != nil {
				validationError = encErr.Error()
				break
			}
			secondaryEncPtr = &enc
		}
		if validationError != "" {
			break
		}
	}

	if validationError != "" {
		primary, advanced := splitFormFields(spec)
		renderForm(w, formModel{
			Title:          "Configure " + spec.DisplayName,
			DisplayName:    spec.DisplayName,
			Description:    spec.Description,
			Scope:          string(spec.Scope),
			Action:         formAction,
			Token:          token,
			Fields:         primary,
			AdvancedFields: advanced,
			Error:          validationError,
		})
		return
	}

	_, err = models.CompletePendingIntegration(
		r.Context(), a.pool, tenant.ID, p.ID,
		usernamePtr, primaryEncPtr, secondaryEncPtr, configMap,
	)
	if err != nil {
		if errors.Is(err, models.ErrPendingNotPending) {
			renderForm(w, formModel{
				Title:       "Configure " + spec.DisplayName,
				DisplayName: spec.DisplayName,
				Scope:       string(spec.Scope),
				Action:      formAction,
				Error:       "This setup link has already been used.",
			})
			return
		}
		slog.Error("completing pending integration", "error", err, "pending_id", p.ID)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	renderForm(w, formModel{
		Title:       "Configured " + spec.DisplayName,
		DisplayName: spec.DisplayName,
		Scope:       string(spec.Scope),
		Success:     true,
		SuccessNote: "You can close this tab and return to your chat.",
	})
}

// encryptSecret returns ciphertext for a plaintext secret value, or a
// user-facing error string wrapped in an error if encryption is
// unavailable. The wrapped string is safe to show to the submitter.
func (a *App) encryptSecret(plain string) (string, error) {
	if a.enc == nil {
		return "", errors.New("server is not configured to accept secrets")
	}
	enc, err := a.enc.Encrypt(plain)
	if err != nil {
		slog.Error("encrypting integration secret", "error", err)
		return "", errors.New("failed to encrypt the secret — try again")
	}
	return enc, nil
}

// verifyAndLoad checks the HMAC, TTL, tenant binding, and pending-row
// existence in one place. Returns the pending row + TypeSpec on success.
func (a *App) verifyAndLoad(ctx context.Context, tenantID uuid.UUID, token string) (*models.PendingIntegration, TypeSpec, error) {
	key := deriveTokenKey(a.tokenSecret())
	if len(key) == 0 {
		return nil, TypeSpec{}, errors.New("server not configured to verify setup links")
	}
	payload, err := verifyToken(key, token)
	if err != nil {
		return nil, TypeSpec{}, fmt.Errorf("invalid token: %w", err)
	}
	if payload.TenantID != tenantID {
		return nil, TypeSpec{}, errors.New("token tenant mismatch")
	}
	p, err := loadPendingAny(ctx, a.pool, tenantID, payload.PendingID)
	if err != nil {
		return nil, TypeSpec{}, err
	}
	if p == nil {
		return nil, TypeSpec{}, errors.New("setup link has expired or was never valid")
	}
	spec, ok := LookupTypeSpec(p.Provider, p.AuthType)
	if !ok {
		return nil, TypeSpec{}, fmt.Errorf("integration type %q is no longer registered", typeKey(p.Provider, p.AuthType))
	}
	return p, spec, nil
}

func (a *App) renderError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	msg := err.Error()
	if isServerErr(err) {
		status = http.StatusInternalServerError
		msg = "Server error — please contact support."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	renderForm(w, formModel{
		Title: "Setup link error",
		Error: msg,
	})
}

func isServerErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.HasPrefix(s, "server ")
}

// splitFormFields returns (primary, advanced) slices built from a spec's
// Fields, preserving declaration order within each group.
func splitFormFields(spec TypeSpec) (primary, advanced []formField) {
	primary = make([]formField, 0, len(spec.Fields))
	for _, f := range spec.Fields {
		t := f.InputType
		if t == "" {
			if f.IsSecret() {
				t = "password"
			} else {
				t = "text"
			}
		}
		label := f.Label
		if label == "" {
			label = f.Name
		}
		ff := formField{
			Name:      f.Name,
			Label:     label,
			InputType: t,
			Required:  f.Required,
			Help:      f.Help,
		}
		if f.Advanced {
			advanced = append(advanced, ff)
		} else {
			primary = append(primary, ff)
		}
	}
	return primary, advanced
}

func displayFieldLabel(f FieldSpec) string {
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

func renderForm(w http.ResponseWriter, m formModel) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := setupTmpl.Execute(w, m); err != nil {
		slog.Error("rendering integration_setup template", "error", err)
	}
}
