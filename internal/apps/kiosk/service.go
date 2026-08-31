// Package kiosk lets a workspace point unattended screens at a URL and change
// that URL later without touching the machine.
//
// The shape is deliberately minimal. A board is a name, a stable key, and a
// URL. Its public endpoint — /{slug}/kiosk/{key} — answers a GET with a 302 to
// the current URL and nothing else: no token, no JSON envelope, no client
// library. A kiosk machine runs a few lines of shell that request that URL
// without following the redirect, read the Location header, and reload the
// browser when it differs from what's on screen. Pointing a plain browser at
// the same URL follows the redirect and lands on the content, which is what
// makes a board usable as a kiosk homepage on first boot.
//
// The endpoint does NOT redirect the poller anywhere it can't come back from,
// which is the one structural rule here: the redirect is data the poller
// reads, never navigation the poller performs.
package kiosk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Validation and lookup failures the console handlers map onto status codes.
var (
	ErrKeyTaken     = errors.New("a board with that key already exists")
	ErrKeyInvalid   = errors.New("key must be 1-40 characters of lowercase letters, numbers, and hyphens")
	ErrNameRequired = errors.New("name is required")
	ErrURLInvalid   = errors.New("url must be an absolute http:// or https:// address")
	ErrNotFound     = errors.New("board not found")
)

// keyPattern is the stable-half contract: lowercase, hyphenated, no slashes.
// It has to survive being typed into a kiosk's browser homepage by hand and
// being read back off a sticky note on the back of a TV.
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Service owns board CRUD and the redirect lookup.
type Service struct {
	pool *pgxpool.Pool
}

// NewService binds a service to the pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// BoardInput is the mutable half of a board, as submitted by an admin.
type BoardInput struct {
	Key   string
	Name  string
	URL   string
	Notes string
}

// normalize trims and lowercases the key, and fills a blank key from the name
// so the common case ("Lobby TV") doesn't force the admin to invent a slug.
func (in *BoardInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	in.Notes = strings.TrimSpace(in.Notes)
	in.Key = strings.ToLower(strings.TrimSpace(in.Key))
	if in.Key == "" {
		in.Key = SlugifyKey(in.Name)
	}
}

// validate enforces the three rules that matter: a key the URL contract can
// carry, a name a human can recognise, and a URL a browser can actually go to.
func (in *BoardInput) validate() error {
	if in.Name == "" {
		return ErrNameRequired
	}
	if !keyPattern.MatchString(in.Key) {
		return ErrKeyInvalid
	}
	if in.URL != "" && !ValidTargetURL(in.URL) {
		return ErrURLInvalid
	}
	return nil
}

// SlugifyKey derives a board key from a display name: lowercase, non
// alphanumerics collapsed to single hyphens, trimmed to the 40-char limit.
// Returns "" when nothing usable survives, which validate() then rejects.
func SlugifyKey(name string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// ValidTargetURL reports whether s is something a kiosk browser can navigate
// to: an absolute http/https URL with a host. Rejecting everything else keeps
// scheme-based surprises (javascript:, data:, file:) out of a field whose
// whole job is to be handed to a browser.
func ValidTargetURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// List returns the tenant's boards.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID) ([]*Board, error) {
	return ListBoards(ctx, s.pool, tenantID)
}

// Create validates and inserts a board.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, in BoardInput) (*Board, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return nil, err
	}
	return InsertBoard(ctx, s.pool, &Board{
		TenantID: tenantID,
		Key:      in.Key,
		Name:     in.Name,
		URL:      in.URL,
		Notes:    in.Notes,
	})
}

// Update validates and writes an existing board. Changing the key is allowed
// but breaks any kiosk already pointed at the old one; the console warns
// about that at the point of edit.
//
// A URL change records what it displaced (see history.go) so a bad paste can
// be undone. That read happens before the write, because afterwards the old
// value is simply gone -- nothing else in the system remembers it.
func (s *Service) Update(ctx context.Context, tenantID, id uuid.UUID, in BoardInput) (*Board, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return nil, err
	}
	previous, err := currentURL(ctx, s.pool, tenantID, id)
	if err != nil {
		return nil, err
	}
	out, err := UpdateBoard(ctx, s.pool, &Board{
		TenantID: tenantID,
		ID:       id,
		Key:      in.Key,
		Name:     in.Name,
		URL:      in.URL,
		Notes:    in.Notes,
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, ErrNotFound
	}
	// Best-effort: the repoint is the job, the history is a convenience.
	if previous != out.URL {
		if err := recordURLChange(ctx, s.pool, tenantID, id, previous); err != nil {
			slog.Warn("recording kiosk url history", "board_id", id, "error", err)
		}
	}
	return out, nil
}

// History returns a board's previous URLs, newest first, for the console's
// rollback list.
func (s *Service) History(ctx context.Context, tenantID, id uuid.UUID) ([]URLChange, error) {
	return ListURLHistory(ctx, s.pool, tenantID, id)
}

// Delete removes a board, reporting ErrNotFound when there was nothing to
// remove.
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	ok, err := DeleteBoard(ctx, s.pool, tenantID, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// Resolve looks up a board for the public endpoint and records the poll.
// Returns ErrNotFound for an unknown key so the handler can 404.
func (s *Service) Resolve(ctx context.Context, tenantID uuid.UUID, key string) (*Board, error) {
	b, err := GetBoardByKey(ctx, s.pool, tenantID, strings.ToLower(strings.TrimSpace(key)))
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, ErrNotFound
	}
	return b, nil
}

// PublicPath is the board's poll/display URL, relative to the deployment's
// base URL. Shown in the console so an admin can copy it onto the machine.
func PublicPath(slug, key string) string {
	return fmt.Sprintf("/%s/kiosk/%s", slug, key)
}
