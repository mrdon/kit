package posters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// The sync lists the photo folder and keeps the index in step with it: new
// or changed files get a pending row with size and provenance from the
// renderer; files gone from Drive become removed. No model is involved;
// describing photos is done from a harness over MCP (core_photos.go).
//
// The hourly run is opt-in per workspace (Settings.AutoSync). The harness
// loop starts with sync_poster_photos anyway, so by default Kit only lists
// Drive when a person or a tool asks.

// SyncResult is what one pass did.
type SyncResult struct {
	Listed    int      `json:"listed"`
	New       int      `json:"new"`
	Inspected int      `json:"inspected"`
	Removed   int      `json:"removed"`
	Pending   int      `json:"pending"`
	Problems  []string `json:"problems"`
}

func (a *App) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "posters.sync_photos",
		Description: "Sync the poster photo index with Google Drive (when auto-sync is on)",
		DefaultCron: "41 * * * *",
		AppliesTo:   a.autoSyncOn,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.SyncPhotos(ctx, job.TenantID)
			return err
		},
	})
	// Unpicked options are candidates, not history. Thirty days is long
	// enough to come back to "the other one" and short enough that the
	// table does not grow with every click of Generate.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "posters.prune_options",
		Description: "Prune unpicked poster options older than 30 days",
		DefaultCron: "23 4 * * *",
		AppliesTo:   a.enabledFor,
		Run: func(ctx context.Context, job models.Job) error {
			n, err := pruneOptions(ctx, a.pool, job.TenantID, 30*24*time.Hour)
			if n > 0 {
				slog.Info("posters: pruned options", "tenant_id", job.TenantID, "count", n)
			}
			return err
		},
	})
}

func (a *App) enabledFor(ctx context.Context, tenantID uuid.UUID) bool {
	return apps.IsEnabled(ctx, tenantID, AppName)
}

// autoSyncOn is the hourly task's AppliesTo: the app is enabled, a photo
// folder is set, and the admin switched the schedule on. Anything else
// retires the row, so a workspace that never opted in has no job at all.
func (a *App) autoSyncOn(ctx context.Context, tenantID uuid.UUID) bool {
	if !a.enabledFor(ctx, tenantID) {
		return false
	}
	s, err := getSettings(ctx, a.pool, tenantID)
	return err == nil && s.PhotoFolderID != "" && s.AutoSync
}

// SyncPhotos runs one pass for a tenant. The result is also recorded on
// the settings row so the admin page can show when it last ran.
func (a *App) SyncPhotos(ctx context.Context, tenantID uuid.UUID) (*SyncResult, error) {
	res, err := a.syncPhotos(ctx, tenantID)
	msg := ""
	if err != nil {
		msg = err.Error()
	} else if len(res.Problems) > 0 {
		msg = strings.Join(res.Problems, "; ")
	}
	if rerr := setSyncResult(ctx, a.pool, tenantID, msg); rerr != nil {
		slog.Warn("posters: recording sync", "error", rerr)
	}
	return res, err
}

func (a *App) syncPhotos(ctx context.Context, tenantID uuid.UUID) (*SyncResult, error) {
	settings, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	if settings.PhotoFolderID == "" {
		return nil, errors.New("no photo folder is set")
	}
	files, err := driveLister{apiKey: a.driveKey}.Walk(ctx, settings.PhotoFolderID)
	if err != nil {
		return nil, err
	}
	res := &SyncResult{Problems: []string{}}
	seen := make([]string, 0, len(files))
	for _, f := range files {
		if !isImageFile(f) {
			continue
		}
		res.Listed++
		seen = append(seen, f.ID)
		photo, changed, err := upsertListedPhoto(ctx, a.pool, tenantID, f.ID, f.Modified, f.Folder, f.Name)
		if err != nil {
			return nil, err
		}
		if photo.CreatedAt.Equal(photo.UpdatedAt) {
			res.New++
		}
		if !changed {
			continue
		}
		if err := a.inspectPhoto(ctx, tenantID, photo); err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("%s: %s", f.Name, truncate(err.Error(), 160)))
			continue
		}
		res.Inspected++
	}
	if res.Listed > 0 {
		if res.Removed, err = markRemovedExcept(ctx, a.pool, tenantID, seen); err != nil {
			return nil, err
		}
	}
	counts, err := photoCounts(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	res.Pending = counts[PhotoPending]
	return res, nil
}

// inspectPhoto asks the renderer for a photo's facts. The renderer
// downloads the file once into its cache, so the first render later is warm.
func (a *App) inspectPhoto(ctx context.Context, tenantID uuid.UUID, p *Photo) error {
	if a.renderer == nil {
		return errors.New("the renderer is not configured")
	}
	info, err := a.renderer.Inspect(ctx, p.Ref())
	if err != nil {
		return err
	}
	return setPhotoFacts(ctx, a.pool, tenantID, p.ID, info.Width, info.Height, info.Orientation, info.C2PA)
}

// SyncNow is the admin button and MCP tool: one pass, right now.
func (a *App) SyncNow(ctx context.Context, tenantID uuid.UUID) (*SyncResult, error) {
	return a.SyncPhotos(ctx, tenantID)
}
