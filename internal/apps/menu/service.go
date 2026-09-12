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

	// ErrNotSynced is a printed menu with nothing in it yet. It is a distinct
	// error from ErrNotFound because the fix is different and worth saying: the
	// tap list is no longer read on the way to the PDF, so a workspace that has
	// a board still has to sync once before it has a sheet.
	ErrNotSynced = errors.New("the printed menu has not been synced yet — press Sync on the printed menu settings")
)

// MaxTaps is what the two-column layout can show at a legible size. Beyond
// this the grid would silently push rows off the bottom of the screen, which
// on a wall display reads as "we stopped serving those" rather than as a bug,
// so it is rejected at the door instead.
//
// It was 18, which was the number before the board could resize itself: the
// row was drawn at one fixed size and the eighteenth beer was the last one
// that fitted. The board now shrinks the whole list until the longest column
// fits its box, so the real ceiling is wherever that pass bottoms out -- and
// 18 had stopped describing anything except the day it was written. A board
// that grew to 20 beers was refused at the door, and refusing a sync leaves
// the LAST good list on the wall: the screen goes on naming beers that blew
// and omitting the ones that replaced them, which is the exact failure the
// cap was put there to prevent, arrived at from the other side.
//
// The number below is measured, in a headless browser at the board's own
// 1920x1080, against tap lists cut every way to the far side of the cliff:
//
//	20 beers  ->  names at 38px, no overflow
//	24 beers  ->  names at 29px, no overflow
//	26 beers  ->  names at 23px, no overflow
//	28 beers  ->  the pass hits its 22px floor and a column overflows
//
// Section headings take column height too, so the cliff moves with how the
// list is divided -- 24 beers across ten sections lands at the same 23px that
// 26 across six does. 24 is the largest count that still fits at every
// division tried, which is what a ceiling has to mean.
const MaxTaps = 24

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
