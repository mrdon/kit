package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxSlugLen keeps generated URLs readable. Titles longer than this are cut at
// a word boundary where possible.
const maxSlugLen = 60

// Slugify turns a title into a URL-safe slug: lowercase ASCII letters, digits,
// and single hyphens.
//
// Non-ASCII input is dropped rather than transliterated -- "Oktoberfest — Bräu"
// becomes "oktoberfest-br". That is lossy but predictable, and the caller
// falls back to a random slug when the result would be empty, so a title of
// pure non-Latin text still gets a usable URL.
func Slugify(title string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are suppressed
	for _, r := range strings.ToLower(title) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > maxSlugLen {
		s = s[:maxSlugLen]
		if i := strings.LastIndexByte(s, '-'); i > 0 {
			s = s[:i]
		}
		s = strings.Trim(s, "-")
	}
	return s
}

// randomSlug is the fallback for titles that slugify to nothing.
func randomSlug() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing is not a condition worth propagating through
		// event creation; a fixed prefix still yields a unique row because
		// UniqueSlug will suffix it on collision.
		return "event"
	}
	return "event-" + hex.EncodeToString(buf[:])
}

// UniqueSlug returns a slug free within the tenant, suffixing -2, -3, ... on
// collision.
//
// excludeID lets an update keep its own slug. Note that cancelled events still
// hold theirs: a slug is a public URL that may already be in an Instagram post,
// so it must never be recycled onto different content.
func UniqueSlug(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, title string, excludeID *uuid.UUID) (string, error) {
	base := Slugify(title)
	if base == "" {
		base = randomSlug()
	}
	candidate := base
	for n := 2; n < 200; n++ {
		taken, err := slugTaken(ctx, pool, tenantID, candidate, excludeID)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	// 200 events sharing one title is not a real condition; fall back to a
	// random slug rather than looping forever.
	return randomSlug(), nil
}

func slugTaken(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slug string, excludeID *uuid.UUID) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM app_events
			WHERE tenant_id = $1 AND slug = $2 AND ($3::uuid IS NULL OR id <> $3)
		)`, tenantID, slug, excludeID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking slug availability: %w", err)
	}
	return exists, nil
}
