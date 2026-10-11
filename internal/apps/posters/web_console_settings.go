package posters

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
)

// The admin page: Drive folders, the logo mapping, the stock switch, the
// derived brand (read-only) and the photo index status.

// logoCandidate is a file in the logo folder, mapped or not.
type logoCandidate struct {
	FileID   string `json:"file_id"`
	Name     string `json:"name"`
	Modified string `json:"modified"`
	Thumb    string `json:"thumb"`
	Variant  string `json:"variant,omitempty"`
}

type settingsPayload struct {
	Settings      Settings        `json:"settings"`
	PhotoFolder   string          `json:"photo_folder_url"`
	LogoFolder    string          `json:"logo_folder_url"`
	LogoFiles     []logoCandidate `json:"logo_files"`
	LogoVariants  []string        `json:"logo_variants"`
	LogoError     string          `json:"logo_error,omitempty"`
	Brand         *BrandStatus    `json:"brand"`
	FontProblems  []string        `json:"font_problems"`
	Counts        map[string]int  `json:"counts"`
	RendererReady bool            `json:"renderer_ready"`
	// RendererConfigured separates "not set up on this server" from
	// "starting"; the UI only promises a retry for the second.
	RendererConfigured bool `json:"renderer_configured"`
	StockEnabled       bool `json:"stock_configured"`
	// DriveAPIKey says the server lists Drive through the API (paginated,
	// with image dimensions) rather than scraping the public folder page,
	// which is what a library beyond a few hundred files needs.
	DriveAPIKey bool   `json:"drive_api_key"`
	IndexPrompt string `json:"index_prompt"`
}

func folderURL(id string) string {
	if id == "" {
		return ""
	}
	return "https://drive.google.com/drive/folders/" + id
}

func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	payload, err := a.settingsPayload(r.Context(), caller.TenantID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) settingsPayload(ctx context.Context, tenantID uuid.UUID) (*settingsPayload, error) {
	s, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	counts, err := photoCounts(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	brand, err := a.brandStatus(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	p := &settingsPayload{
		Settings: s, PhotoFolder: folderURL(s.PhotoFolderID), LogoFolder: folderURL(s.LogoFolderID),
		Brand: brand, FontProblems: []string{}, LogoFiles: []logoCandidate{}, LogoVariants: []string{},
		Counts:        map[string]int{"pending": counts[PhotoPending], "indexed": counts[PhotoIndexed], "removed": counts[PhotoRemoved]},
		RendererReady: a.RendererReady(ctx), RendererConfigured: a.renderer != nil, StockEnabled: a.stockConfigured(ctx, tenantID), DriveAPIKey: a.driveKey != "",
		IndexPrompt: "Load the indexing-poster-photos skill, then list_pending_photos and describe them in batches with get_photo_sheet and index_photos until none are pending.",
	}
	if brand.Brand != nil {
		p.LogoVariants = logoVariants(brand)
		if a.renderer != nil {
			if problems, err := a.renderer.BrandCheck(ctx, *brand.Brand); err == nil {
				p.FontProblems = problems
			}
		}
	}
	if s.LogoFolderID != "" {
		p.LogoFiles, p.LogoError = a.logoFiles(ctx, s)
		// Map by filename whenever a wanted variant is still missing, not
		// only on Save: the mapping rule can improve after a folder was
		// saved, and the page should show the result without a second click.
		if p.LogoError == "" && a.mapMissingLogos(ctx, &s, p.LogoFiles, p.LogoVariants) {
			p.Settings = s
			p.LogoFiles, p.LogoError = a.logoFiles(ctx, s)
		}
	}
	return p, nil
}

// mapMissingLogos runs the filename mapping against the folder's files for
// any wanted variant without a file, persists a change, and says whether
// it made one.
func (a *App) mapMissingLogos(ctx context.Context, s *Settings, files []logoCandidate, wanted []string) bool {
	missing := false
	for _, v := range wanted {
		if cur, ok := s.LogoMap[v]; !ok || cur.FileID == "" {
			missing = true
		}
	}
	if !missing || len(files) == 0 {
		return false
	}
	drive := make([]DriveFile, 0, len(files))
	for _, f := range files {
		drive = append(drive, DriveFile{ID: f.FileID, Name: f.Name, Modified: f.Modified, Mime: "image/"})
	}
	before := len(s.LogoMap)
	autoMapLogos(drive, s.LogoMap, wanted)
	if len(s.LogoMap) == before {
		return false
	}
	saved, err := upsertSettings(ctx, a.pool, *s)
	if err != nil {
		slog.Warn("posters: saving logo mapping", "error", err)
		return false
	}
	*s = saved
	return true
}

// logoVariants are the variant names the grounds call for, so the mapping
// UI offers exactly those.
func logoVariants(b *BrandStatus) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range sortedKeys(b.Brand.Grounds) {
		v := b.Brand.Grounds[name].Logo
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (a *App) logoFiles(ctx context.Context, s Settings) ([]logoCandidate, string) {
	files, err := driveLister{apiKey: a.driveKey}.Walk(ctx, s.LogoFolderID)
	if err != nil {
		return []logoCandidate{}, err.Error()
	}
	byFile := map[string]string{}
	for variant, f := range s.LogoMap {
		byFile[f.FileID] = variant
	}
	out := []logoCandidate{}
	for _, f := range files {
		if !isImageFile(f) {
			continue
		}
		out = append(out, logoCandidate{FileID: f.ID, Name: f.Name, Modified: f.Modified, Thumb: driveThumbnailURL(f.ID, 300), Variant: byFile[f.ID]})
	}
	return out, ""
}

type settingsBody struct {
	PhotoFolder      string              `json:"photo_folder_url"`
	LogoFolder       string              `json:"logo_folder_url"`
	LogoMap          map[string]LogoFile `json:"logo_map"`
	AllowStockPhotos bool                `json:"allow_stock_photos"`
	AutoSync         bool                `json:"auto_sync"`
}

// variantInName finds a variant word in a logo filename. Underscores count
// as word characters to \b, so "logo_white_transparent.png" needs the
// explicit separator class on both sides.
var variantInName = regexp.MustCompile(`(?i)(?:^|[-_ ])(color|colour|white|black|forest|reversed|mono)(?:[-_ .]|$)`)

// handleSaveSettings stores the folders and mapping. A folder is checked by
// listing it, and logo files named after a variant map themselves when no
// mapping was given for that variant.
func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body settingsBody
	if !readBody(w, r, &body) {
		return
	}
	photoID, err := driveFolderID(body.PhotoFolder)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "photo folder: "+err.Error())
		return
	}
	logoID, err := driveFolderID(body.LogoFolder)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "logo folder: "+err.Error())
		return
	}
	lister := driveLister{apiKey: a.driveKey}
	if photoID != "" {
		if _, err := lister.list(r.Context(), photoID); err != nil {
			writeErr(w, http.StatusBadRequest, "photo folder: "+err.Error())
			return
		}
	}
	logoMap := body.LogoMap
	if logoMap == nil {
		logoMap = map[string]LogoFile{}
	}
	if logoID != "" {
		files, err := lister.Walk(r.Context(), logoID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "logo folder: "+err.Error())
			return
		}
		var wanted []string
		if st, err := a.brandStatus(r.Context(), caller.TenantID); err == nil && st.Brand != nil {
			wanted = logoVariants(st)
		}
		autoMapLogos(files, logoMap, wanted)
	}
	s := Settings{TenantID: caller.TenantID, PhotoFolderID: photoID, LogoFolderID: logoID, LogoMap: logoMap, AllowStockPhotos: body.AllowStockPhotos, AutoSync: body.AutoSync}
	if _, err := upsertSettings(r.Context(), a.pool, s); err != nil {
		a.httpErr(w, err)
		return
	}
	payload, err := a.settingsPayload(r.Context(), caller.TenantID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// autoMapLogos fills unmapped variants from filenames like
// gravity-white.png; a mapping an admin made by hand is kept. When the
// names do not say and exactly one wanted variant and one image file are
// left over, they are paired: a folder with "logo.png" and
// "logo_white.png" needs no further help.
func autoMapLogos(files []DriveFile, logoMap map[string]LogoFile, wanted []string) {
	used := map[string]bool{}
	for _, f := range logoMap {
		used[f.FileID] = true
	}
	var leftover []DriveFile
	for _, f := range files {
		if !isImageFile(f) || used[f.ID] {
			continue
		}
		m := variantInName.FindStringSubmatch(f.Name)
		if m == nil {
			leftover = append(leftover, f)
			continue
		}
		variant := strings.ToLower(m[1])
		switch variant {
		case "colour":
			variant = "color"
		case "reversed":
			variant = "white"
		case "mono":
			variant = "black"
		}
		if cur, ok := logoMap[variant]; ok && cur.FileID != "" {
			leftover = append(leftover, f)
			continue
		}
		logoMap[variant] = LogoFile{FileID: f.ID, Name: f.Name, Modified: f.Modified}
		used[f.ID] = true
	}
	var missing []string
	for _, v := range wanted {
		if cur, ok := logoMap[v]; !ok || cur.FileID == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) == 1 && len(leftover) == 1 {
		logoMap[missing[0]] = LogoFile{FileID: leftover[0].ID, Name: leftover[0].Name, Modified: leftover[0].Modified}
	}
}

func (a *App) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	res, err := a.SyncNow(r.Context(), caller.TenantID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res})
}

// handleRederive clears the cached brand so the next read derives it
// again, for when the guide changed in a way the hash did not catch (a
// model extraction worth retrying).
func (a *App) handleRederive(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if err := setBrand(r.Context(), a.pool, caller.TenantID, nil, "", nil); err != nil {
		a.httpErr(w, err)
		return
	}
	payload, err := a.settingsPayload(r.Context(), caller.TenantID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
