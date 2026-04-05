package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// RegistrationHandler implements RFC 7591 Dynamic Client Registration.
// Claude Code uses this to register itself as an OAuth client.
type RegistrationHandler struct {
	pool *pgxpool.Pool
}

// NewRegistrationHandler creates a new registration handler.
func NewRegistrationHandler(pool *pgxpool.Pool) *RegistrationHandler {
	return &RegistrationHandler{pool: pool}
}

// HandleRegister processes a client registration request.
func (h *RegistrationHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	clientID := randomString(16)
	clientSecret := randomString(32)

	client, err := models.CreateOAuthClient(r.Context(), h.pool, clientID, clientSecret, req.RedirectURIs, req.ClientName)
	if err != nil {
		slog.Error("creating oauth client", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"client_id":      client.ClientID,
		"client_secret":  client.ClientSecret,
		"redirect_uris":  client.RedirectURIs,
		"client_name":    client.ClientName,
		"grant_types":    []string{"authorization_code"},
		"response_types": []string{"code"},
	})
}
