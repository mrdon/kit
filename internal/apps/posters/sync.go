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

// SyncResult is what one pass did. Listing and recording are quick and
// happen before the result returns; inspecting new files means downloading
// each one through the renderer, so that continues in the background and
// Inspecting says how many are on their way.
type SyncResult struct {
	Listed     int      `json:"listed"`
	New        int      `json:"new"`
	Inspecting int      `json:"inspecting"`
	Removed    int      `json:"removed"`
	Pending    int      `json:"pending"`
	Problems   []string `json:"problems"`
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
// the settings row so the admin page can show when it last ran. New or
// changed files are inspected after the result returns, detached from the
// caller's request: an MCP client or a browser gives up long before a
// library of a few dozen photos has downloaded, and a cancelled request
// used to leave the tail of the list without dimensions.
func (a *App) SyncPhotos(ctx context.Context, tenantID uuid.UUID) (*SyncResult, error) {
	res, toInspect, err := a.syncPhotos(ctx, tenantID)
	msg := ""
	if err != nil {
		msg = err.Error()
	} else if len(res.Problems) > 0 {
		msg = strings.Join(res.Problems, "; ")
	}
	if rerr := setSyncResult(ctx, a.pool, tenantID, msg); rerr != nil {
		slog.Warn("posters: recording sync", "error", rerr)
	}
	if err == nil && len(toInspect) > 0 {
		a.inspectDetached(ctx, tenantID, toInspect)
	}
	return res, err
}

// inspectDetached downloads and measures new photos after the sync has
// answered. One pass per tenant at a time; a file this pass misses still
// has no width, so the next sync picks it up.
func (a *App) inspectDetached(ctx context.Context, tenantID uuid.UUID, photos []*Photo) {
	if _, busy := a.inspecting.LoadOrStore(tenantID, true); busy {
		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
	go func() {
		defer cancel()
		defer a.inspecting.Delete(tenantID)
		var problems []string
		for _, p := range photos {
			if err := a.inspectPhoto(bg, tenantID, p); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s", p.Filename, truncate(err.Error(), 160)))
			}
		}
		slog.Info("posters: inspected photos", "tenant_id", tenantID, "count", len(photos)-len(problems), "problems", len(problems))
		if len(problems) > 0 {
			if err := setSyncResult(bg, a.pool, tenantID, strings.Join(problems, "; ")); err != nil {
				slog.Warn("posters: recording inspection problems", "error", err)
			}
		}
	}()
}

// syncPhotos lists the folder and records what is there. It returns the
// photos that still need inspecting rather than inspecting them.
func (a *App) syncPhotos(ctx context.Context, tenantID uuid.UUID) (*SyncResult, []*Photo, error) {
	settings, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if settings.PhotoFolderID == "" {
		return nil, nil, errors.New("no photo folder is set")
	}
	files, err := driveLister{apiKey: a.driveKey}.Walk(ctx, settings.PhotoFolderID)
	if err != nil {
		return nil, nil, err
	}
	res := &SyncResult{Problems: []string{}}
	seen := make([]string, 0, len(files))
	var toInspect []*Photo
	for _, f := range files {
		if !isImageFile(f) {
			continue
		}
		res.Listed++
		seen = append(seen, f.ID)
		photo, changed, err := upsertListedPhoto(ctx, a.pool, tenantID, f.ID, f.Modified, f.Folder, f.Name)
		if err != nil {
			return nil, nil, err
		}
		if photo.CreatedAt.Equal(photo.UpdatedAt) {
			res.New++
		}
		if changed {
			toInspect = append(toInspect, photo)
		}
	}
	if a.renderer == nil && len(toInspect) > 0 {
		res.Problems = append(res.Problems, "the renderer is not configured, so new photos cannot be measured")
		toInspect = nil
	}
	res.Inspecting = len(toInspect)
	if res.Listed > 0 {
		if res.Removed, err = markRemovedExcept(ctx, a.pool, tenantID, seen); err != nil {
			return nil, nil, err
		}
	}
	counts, err := photoCounts(ctx, a.pool, tenantID)
	if err != nil {
		return nil, nil, err
	}
	res.Pending = counts[PhotoPending]
	return res, toInspect, nil
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
