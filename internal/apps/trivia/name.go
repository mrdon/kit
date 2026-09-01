package trivia

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// randomName draws a verb and an animal: jumping-lion.
//
// crypto/rand not for secrecy -- the name is painted on a TV -- but because
// a process restarting twice during a deploy must not hand two games the same
// seed. math/rand seeded from the clock does exactly that.
func randomName() string {
	return pick(verbs) + "-" + pick(animals)
}

func pick(words []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		// crypto/rand failing is a broken machine, not a case to handle;
		// falling back to a fixed word keeps the caller's uniqueness retry
		// working rather than failing a game creation outright.
		return words[0]
	}
	return words[n.Int64()]
}

// UniqueName returns a game name not already used in this workspace.
//
// The loop is an optimisation, not the guarantee: UNIQUE (tenant_id, name)
// plus a retry on 23505 at insert time is what actually keeps two names from
// colliding. Ten draws from sixty-odd thousand combinations makes reaching
// the numeric suffix something a venue would have to run thousands of quizzes
// to see.
//
// NAMES ARE NEVER RECYCLED. Same reasoning as an event slug that may already
// be on a poster -- here, on a whiteboard behind the bar, or in a photo
// somebody took of the TV.
func UniqueName(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (string, error) {
	for range 10 {
		name := randomName()
		taken, err := nameTaken(ctx, pool, tenantID, name)
		if err != nil {
			return "", err
		}
		if !taken {
			return name, nil
		}
	}
	// Ten collisions means the bank of names is genuinely crowded for this
	// workspace. Suffixing keeps the name readable rather than falling back
	// to something with a UUID in it that nobody can type.
	base := randomName()
	for n := 2; n < 100; n++ {
		name := fmt.Sprintf("%s-%d", base, n)
		taken, err := nameTaken(ctx, pool, tenantID, name)
		if err != nil {
			return "", err
		}
		if !taken {
			return name, nil
		}
	}
	return "", errors.New("could not find an unused trivia game name")
}

func nameTaken(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM app_trivia_games WHERE tenant_id = $1 AND name = $2)`,
		tenantID, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking trivia game name: %w", err)
	}
	return exists, nil
}

// IsValidGameName guards the public URL segment. Its ONLY job is to reject
// junk before it reaches a query -- path traversal, uppercase, empties -- and
// to stay unambiguous against the literal routes beside it
// (/{slug}/trivia/tv), which a single word cannot shadow.
//
// It deliberately does NOT assert the current generator's shape. Names are
// permanent and public: they are on a whiteboard behind the bar and in the
// URL a TV is parked on. Tightening this to "exactly two words" when the
// generator moved from three-word names to two 404'd every game that already
// existed -- their screens, their join links, all of it -- which is exactly
// the failure a validator running against historical rows must not cause.
//
// So: two to four lowercase words, with an optional trailing numeric suffix
// from the collision path. Wide enough to cover every scheme this has used
// and any near neighbour of it.
func IsValidGameName(s string) bool {
	if len(s) == 0 || len(s) > 60 {
		return false
	}
	parts := strings.Split(s, "-")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for i, p := range parts {
		if p == "" {
			return false
		}
		// A trailing all-digit part is the collision suffix. Anywhere else,
		// and anything else, must be a lowercase word.
		if i == len(parts)-1 && i > 0 && isDigits(p) {
			continue
		}
		for _, r := range p {
			if r < 'a' || r > 'z' {
				return false
			}
		}
	}
	return true
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}
