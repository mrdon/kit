package googlecalendar

import (
	"crypto/sha1" //nolint:gosec // sha1 used for a stable id digest, not security
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// lowerHex32 is base32hex (RFC 4648 "extended hex" alphabet, 0-9a-v)
// without padding — exactly the character set Google Calendar allows for a
// client-specified event id (lowercase a-v + digits, length 5-1024).
var lowerHex32 = base32.HexEncoding.WithPadding(base32.NoPadding)

// DeterministicID derives a stable, valid Google Calendar event id from an
// arbitrary seed (e.g. "square:<shiftID>"). Same seed → same id, so
// re-syncing upserts rather than duplicating. Output is 32 lowercase
// base32hex chars from a SHA-1 digest of the seed.
func DeterministicID(seed string) string {
	sum := sha1.Sum([]byte(seed)) //nolint:gosec // not a security context
	return strings.ToLower(lowerHex32.EncodeToString(sum[:]))
}

// probeEventID builds a deterministic id for the connection-check probe so
// repeated checks reuse (and clean up) the same event rather than piling up.
func probeEventID(tenantID uuid.UUID, start time.Time) string {
	return DeterministicID(fmt.Sprintf("kit-check:%s:%d", tenantID, start.Unix()))
}
