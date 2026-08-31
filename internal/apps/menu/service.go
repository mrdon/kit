package menu

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Validation and lookup failures the handlers and tools map onto status codes
// and user-facing messages.
var (
	ErrKeyTaken       = errors.New("a menu board with that key already exists")
	ErrKeyInvalid     = errors.New("key must be 1-40 characters of lowercase letters, numbers, and hyphens")
	ErrNameRequired   = errors.New("name is required")
	ErrPayloadInvalid = errors.New("menu payload is invalid")
	ErrNotFound       = errors.New("menu board not found")
)

// MaxTaps is what the two-column layout can show at a legible size. Beyond
// this the grid would silently push rows off the bottom of the screen, which
// on a wall display reads as "we stopped serving those" rather than as a bug,
// so it is rejected at the door instead.
const MaxTaps = 18

// DefaultKey is the workspace's one menu. It is not something anyone creates:
// every workspace has a menu address from the moment the app is on, served at
// /{slug}/menu with no key in it, and setting the taps is an update to
// content that already has a home rather than an act of publication.
//
// Extra keyed boards remain possible for a second screen showing something
// different, but they are the exception and they carry their key in the path.
const DefaultKey = "default"

// keyPattern matches the kiosk app's contract: lowercase, hyphenated, no
// slashes. A menu key ends up pasted into a kiosk board's URL field by hand.
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Service owns board lookup and the upsert the push path uses.
type Service struct {
	pool *pgxpool.Pool
}

// NewService binds a service to the pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// BoardInput is one whole board as submitted by an author.
type BoardInput struct {
	Key     string
	Name    string
	Payload []byte
}

func (in *BoardInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Key = strings.ToLower(strings.TrimSpace(in.Key))
	// No key means the workspace's menu, not a new board named after
	// whatever label was typed. Slugifying the name here is how you end up
	// with a fresh URL every time someone rewords the heading.
	if in.Key == "" {
		in.Key = DefaultKey
	}
	if in.Name == "" {
		in.Name = "Menu"
	}
}

func (in *BoardInput) validate() error {
	if in.Name == "" {
		return ErrNameRequired
	}
	if !keyPattern.MatchString(in.Key) {
		return ErrKeyInvalid
	}
	_, err := ParseBoard(in.Payload)
	return err
}

// SlugifyKey derives a board key from a display name, matching the kiosk
// app's rules so the two feel like one system to whoever types them.
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

// Save upserts a whole board by key. Authoring happens elsewhere and pushes
// the document up entire, so there is no partial update: a push either
// replaces the board or fails validation and changes nothing.
func (s *Service) Save(ctx context.Context, tenantID uuid.UUID, in BoardInput) (*BoardRow, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return nil, err
	}
	return UpsertBoard(ctx, s.pool, &BoardRow{
		TenantID: tenantID,
		Key:      in.Key,
		Name:     in.Name,
		Payload:  in.Payload,
	})
}

// Get loads one board by key, returning ErrNotFound for an unknown key.
func (s *Service) Get(ctx context.Context, tenantID uuid.UUID, key string) (*BoardRow, error) {
	row, err := GetBoardByKey(ctx, s.pool, tenantID, strings.ToLower(strings.TrimSpace(key)))
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotFound
	}
	return row, nil
}

// List returns the tenant's boards.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID) ([]*BoardRow, error) {
	return ListBoards(ctx, s.pool, tenantID)
}

// Delete removes a board, reporting ErrNotFound when there was nothing to
// remove.
func (s *Service) Delete(ctx context.Context, tenantID uuid.UUID, key string) error {
	ok, err := DeleteBoard(ctx, s.pool, tenantID, strings.ToLower(strings.TrimSpace(key)))
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// PublicPath is the board's display URL, relative to the deployment's base
// URL. This is the string an admin copies into a kiosk board.
//
// The default board has no key in its path: a workspace with one menu should
// have one obvious address, not one with "default" stuck on the end.
func PublicPath(slug, key string) string {
	if key == "" || key == DefaultKey {
		return fmt.Sprintf("/%s/menu", slug)
	}
	return fmt.Sprintf("/%s/menu/%s", slug, key)
}
