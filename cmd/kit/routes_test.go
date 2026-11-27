package main

import (
	"net/http"
	"testing"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/testdb"
)

// Every app's routes go on one mux at startup, and Go's ServeMux panics on
// two patterns that can match the same path with neither more specific. That
// panic happens only at boot, so a clash between two apps (or two files of
// one app) passes every package's own tests and fails the deploy instead.
// Registering them all here, after Init as main does (some apps mount
// nothing without a pool), catches it first.
func TestAppRoutesDoNotConflict(t *testing.T) {
	pool := testdb.Open(t)
	apps.Init(pool)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registering app routes: %v", r)
		}
	}()
	apps.RegisterAllRoutes(http.NewServeMux(), pool)
}
