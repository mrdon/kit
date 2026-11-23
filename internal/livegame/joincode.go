package livegame

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// joinCodeAlphabet is base32 with the characters that get misread stripped
// out: no i or l (they look like 1), no o (looks like 0), no u (heard as
// "you" when a code is read across a room).
const joinCodeAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// joinCodeLength is five characters, about a million codes. The unique index
// plus a retry is the guarantee; this is the size that keeps the URL short
// enough to matter for the QR.
const joinCodeLength = 5

// NewJoinCode draws a code. crypto/rand so a process restarting twice during
// a deploy cannot hand two games the same seed.
func NewJoinCode() string {
	var b strings.Builder
	for range joinCodeLength {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(joinCodeAlphabet))))
		if err != nil {
			return ""
		}
		b.WriteByte(joinCodeAlphabet[n.Int64()])
	}
	return b.String()
}

// IsValidJoinCode guards a root-level join route before it reaches a query.
func IsValidJoinCode(s string) bool {
	if len(s) < 4 || len(s) > 12 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(joinCodeAlphabet, r) {
			return false
		}
	}
	return true
}

// AssignJoinCode draws codes and hands each to store until one is not a
// unique violation. Ten draws from a million is not a loop that runs twice in
// practice; the unique index is the real guarantee.
func AssignJoinCode(store func(code string) error) (string, error) {
	for range 10 {
		code := NewJoinCode()
		if code == "" {
			return "", errors.New("could not generate a join code")
		}
		err := store(code)
		if err == nil {
			return code, nil
		}
		if !IsUniqueViolation(err) {
			return "", fmt.Errorf("assigning join code: %w", err)
		}
	}
	return "", errors.New("could not find an unused join code")
}

// ShortJoinURL is what the QR encodes and what the TV prints: /j/{game}/{code}
// at the root, where game is the app's one-letter route ("t" for trivia).
//
// It is deliberately the SHORTEST correct thing: no workspace, no game name.
// QR jumps a version band at 33 characters, and the modules are what decide
// whether a phone picks the code up from across a room. Three segments
// rather than /j/{code} because Go's mux rejects that one: it overlaps
// /{slug}/mcp and neither is more specific.
func ShortJoinURL(baseURL, game, code string) string {
	return strings.TrimRight(baseURL, "/") + "/j/" + game + "/" + code
}

// uniqueViolation is Postgres 23505.
const uniqueViolation = "23505"

// IsUniqueViolation reports whether err is a Postgres unique violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
