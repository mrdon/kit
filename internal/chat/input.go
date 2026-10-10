package chat

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	store "github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
)

// Input is the parsed body of a chat-execute request, shared by every
// chat surface (card chat, quick chat, poster chat).
type Input struct {
	Text string `json:"text"`
	// ClientSessionID is required for the quick-chat (card-less) path
	// and ignored for card chat. The client mints a UUID on sheet-open
	// so fresh per open / multi-turn within open works without server
	// state.
	ClientSessionID string `json:"client_session_id,omitempty"`
	// PageContext is an optional human-readable description of where the
	// user is in the web console (e.g. "the Tasks page"). Quick chat only;
	// ignored for card chat. Drives the agent's "this"/"here" resolution.
	PageContext string `json:"page_context,omitempty"`
}

// maxAttachments caps files per chat turn (the manifest + per-image cost
// would otherwise be unbounded).
const maxAttachments = 10

// maxMultipartBytes bounds the whole multipart form in memory.
const maxMultipartBytes = 60 << 20 // 60 MiB

// ReadInput parses the chat-execute request. JSON bodies carry text
// only; multipart bodies additionally carry files, which are stored
// immediately and returned as agent attachment refs. On any failure it
// writes the HTTP error and returns ok=false. Must run before the SSE
// stream opens.
func ReadInput(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, enc *crypto.Encryptor, caller *services.Caller) (req Input, attachments []agent.AttachmentRef, ok bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return readMultipartInput(w, r, pool, enc, caller)
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("reading chat execute body", "error", err, "content_length", r.ContentLength)
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return req, nil, false
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Text == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return req, nil, false
	}
	return req, nil, true
}

func readMultipartInput(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, enc *crypto.Encryptor, caller *services.Caller) (req Input, attachments []agent.AttachmentRef, ok bool) {
	if enc == nil {
		http.Error(w, "attachments not configured", http.StatusInternalServerError)
		return req, nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBytes)
	if err := r.ParseMultipartForm(maxMultipartBytes); err != nil {
		http.Error(w, "upload too large or malformed", http.StatusRequestEntityTooLarge)
		return req, nil, false
	}
	req.Text = r.FormValue("text")
	req.ClientSessionID = r.FormValue("client_session_id")
	req.PageContext = r.FormValue("page_context")

	files := r.MultipartForm.File["files"]
	if len(files) > maxAttachments {
		http.Error(w, "too many attachments", http.StatusRequestEntityTooLarge)
		return req, nil, false
	}
	svc := store.NewService(pool, enc)
	for _, fh := range files {
		raw, err := readUpload(fh)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return req, nil, false
		}
		mime := fh.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}
		att, err := svc.Store(r.Context(), caller.TenantID, caller.UserID, fh.Filename, mime, raw)
		if err != nil {
			slog.Warn("storing chat attachment", "error", err, "filename", fh.Filename)
			http.Error(w, "could not store attachment", http.StatusInternalServerError)
			return req, nil, false
		}
		attachments = append(attachments, agent.AttachmentRef{
			ID: att.ID.String(), Filename: att.Filename, Mime: att.Mime, Size: att.Size,
		})
	}

	if req.Text == "" && len(attachments) == 0 {
		http.Error(w, "text or attachment required", http.StatusBadRequest)
		return req, nil, false
	}
	return req, attachments, true
}

// readUpload reads one multipart file, enforcing the per-file size cap.
func readUpload(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, errors.New("bad upload")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, store.MaxBytes+1))
	if err != nil {
		return nil, errors.New("bad upload")
	}
	if len(raw) > store.MaxBytes {
		return nil, errors.New("attachment too large")
	}
	return raw, nil
}
