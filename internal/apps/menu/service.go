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
	ErrKeyInvalid     = errors.New("key must be 1-40 characters of lowercase letters, numbers, and hyphens")
	ErrPayloadInvalid = errors.New("menu payload is invalid")
	ErrNotFound       = errors.New("this workspace has no menu yet")
)

// MaxTaps is what the two-column layout can show at a legible size. Beyond
// this the grid would silently push rows off the bottom of the screen, which
// on a wall display reads as "we stopped serving those" rather than as a bug,
// so it is rejected at the door instead.
const MaxTaps = 18

// keyPattern constrains asset keys: lowercase, hyphenated, no slashes, so one
// can be typed into a payload by hand without surprises.
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Service owns the workspace's menu.
//
// There is exactly one per workspace. An earlier version let a workspace keep
// several boards under different keys, which cost more than it looked: two
// addresses to keep straight, a key in every tool call, a console listing for
// a list that was always one long, and a way to create a board by accident
// that then needed deleting. A workspace has a menu.
type Service struct {
	pool *pgxpool.Pool
}

// NewService binds a service to the pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Save replaces the workspace's tap list.
//
// Authoring happens elsewhere and pushes the document up entire, so there is
// no partial update: a save either replaces the menu or fails validation and
// changes nothing.
func (s *Service) Save(ctx context.Context, tenantID uuid.UUID, name string, payload []byte) (*BoardRow, error) {
	if _, err := ParseBoard(payload); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Menu"
	}
	return UpsertBoard(ctx, s.pool, tenantID, name, payload)
}

// Get loads the workspace's menu, returning ErrNotFound when none is set.
func (s *Service) Get(ctx context.Context, tenantID uuid.UUID) (*BoardRow, error) {
	row, err := GetBoard(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotFound
	}
	return row, nil
}

// PublicPath is the menu's display URL, relative to the deployment's base
// URL. This is the string an admin copies into a kiosk screen.
func PublicPath(slug string) string {
	return fmt.Sprintf("/%s/menu", slug)
}
