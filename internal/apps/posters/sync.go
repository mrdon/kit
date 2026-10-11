package posters

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// The sync lists the photo folder and keeps the index in step with it: new
// or changed files get a pending row, files gone from Drive become
// removed, and a separate measure task fills in size and provenance a
// batch at a time. No model is involved; describing photos is done from a
// harness over MCP (core_photos.go).
//
// The hourly sync is opt-in per workspace (Settings.AutoSync). The harness
// loop starts with sync_poster_photos anyway, so by default Kit only lists
// Drive when a person or a tool asks.

// SyncResult is what one pass did. Listing and recording are quick and
// finish before the result returns. Measuring a photo (size, orientation,
// provenance) can mean downloading it, so that is the measure task's job,
// a few minutes at a time; ToMeasure says how many are waiting for it. The
// Drive API reports dimensions in the listing, so with a key almost
// nothing waits.
type SyncResult struct {
	Listed    int      `json:"listed"`
	New       int      `json:"new"`
	ToMeasure int      `json:"to_measure"`
	Removed   int      `json:"removed"`
	Pending   int      `json:"pending"`
	Problems  []string `json:"problems"`
}

// measureBatch is how many photos one run of the measure task downloads.
// Five minutes apart, forty a run keeps a first sync of a few thousand
// photos moving without holding the function lane for long.
const measureBatch = 40

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
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "posters.measure_photos",
		Description: "Measure new poster photos (size, orientation, provenance)",
		DefaultCron: "*/5 * * * *",
		AppliesTo:   a.photoFolderConfigured,
		Run: func(ctx context.Context, job models.Job) error {
			return a.measurePhotos(ctx, job.TenantID, measureBatch)
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

// photoFolderConfigured: the app is on and a photo folder is set.
func (a *App) photoFolderConfigured(ctx context.Context, tenantID uuid.UUID) bool {
	if !a.enabledFor(ctx, tenantID) {
		return false
	}
	s, err := getSettings(ctx, a.pool, tenantID)
	return err == nil && s.PhotoFolderID != ""
}

// autoSyncOn is the hourly task's AppliesTo: a folder is set and the admin
// switched the schedule on. Anything else retires the row, so a workspace
// that never opted in has no job at all.
func (a *App) autoSyncOn(ctx context.Context, tenantID uuid.UUID) bool {
	if !a.photoFolderConfigured(ctx, tenantID) {
		return false
	}
	s, err := getSettings(ctx, a.pool, tenantID)
	return err == nil && s.AutoSync
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

// syncPhotos lists the folder tree and records it in a handful of
// statements, whatever the library's size. Nothing here downloads a photo.
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
	images := make([]DriveFile, 0, len(files))
	seen := make([]string, 0, len(files))
	for _, f := range files {
		if isImageFile(f) {
			images = append(images, f)
			seen = append(seen, f.ID)
		}
	}
	res := &SyncResult{Listed: len(images), Problems: []string{}}
	if res.New, err = recordListedPhotos(ctx, a.pool, tenantID, images); err != nil {
		return nil, err
	}
	if res.Listed > 0 {
		if res.Removed, err = markRemovedExcept(ctx, a.pool, tenantID, seen); err != nil {
			return nil, err
		}
	}
	if res.ToMeasure, err = countUnmeasured(ctx, a.pool, tenantID); err != nil {
		return nil, err
	}
	if res.ToMeasure > 0 && a.renderer == nil {
		res.Problems = append(res.Problems, "the renderer is not configured, so new photos cannot be measured")
	}
	counts, err := photoCounts(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	res.Pending = counts[PhotoPending]
	return res, nil
}

// measurePhotos is one run of the measure task: up to limit photos without
// dimensions, each downloaded once through the renderer. A failure is
// logged and the photo moves to the back of the queue rather than stopping
// the run; nothing downstream waits on a measurement, since describing
// uses thumbnails and renders measure the original themselves.
func (a *App) measurePhotos(ctx context.Context, tenantID uuid.UUID, limit int) error {
	if a.renderer == nil {
		return nil
	}
	photos, err := listUnmeasured(ctx, a.pool, tenantID, limit)
	if err != nil {
		return err
	}
	done, failed := 0, 0
	for i := range photos {
		if err := a.inspectPhoto(ctx, tenantID, &photos[i]); err != nil {
			failed++
			slog.Warn("posters: measuring photo", "tenant_id", tenantID, "photo_id", photos[i].ID, "file", photos[i].Filename, "error", err)
			if terr := touchPhoto(ctx, a.pool, tenantID, photos[i].ID); terr != nil {
				return terr
			}
			continue
		}
		done++
	}
	if done+failed > 0 {
		slog.Info("posters: measured photos", "tenant_id", tenantID, "measured", done, "failed", failed)
	}
	return nil
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
