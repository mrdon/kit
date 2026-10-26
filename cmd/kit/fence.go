package main

import (
	"context"
	"os"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/buildinfo"
	"github.com/mrdon/kit/internal/fence"
)

// startFence registers this process and returns the fence the scheduler
// gates on. When a newer build has been up long enough, the fence retires
// this process through the same SIGTERM path as `docker stop`, so it shuts
// down cleanly and exits 0, which a restart-on-failure policy leaves down.
func startFence(ctx context.Context, pool *pgxpool.Pool) *fence.Fence {
	var builtAt *time.Time
	if t, ok := buildinfo.BuiltAt(); ok {
		builtAt = &t
	}
	f := fence.New(pool, buildinfo.Token(), builtAt, func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	})
	f.Start(ctx)
	return f
}
