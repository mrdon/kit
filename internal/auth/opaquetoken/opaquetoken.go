// Package opaquetoken mints random bearer secrets and hashes them for
// storage. An API token, a session cookie's backing row, a widget token
// and a trivia team cookie are all the same thing: 256 random bits whose
// SHA-256 is the only form a database row ever holds.
//
// The token is the credential; the hash is the lookup key. A database
// dump is therefore not a set of usable credentials, and a leaked token
// is found by hashing it, never by storing it.
package opaquetoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// New mints a token of 32 random bytes, hex-encoded behind prefix, and
// returns it with its hex-encoded SHA-256. The prefix ("kit_") lets a
// human tell a Kit token from any other hex blob in a log or a paste.
func New(prefix string) (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating random bytes: %w", err)
	}
	token = prefix + hex.EncodeToString(b)
	return token, Hash(token), nil
}

// Hash is the hex-encoded SHA-256 of a token: what a TEXT column stores.
func Hash(token string) string {
	return hex.EncodeToString(Sum(token))
}

// Sum is the raw SHA-256 of a token: what a BYTEA column stores.
func Sum(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
