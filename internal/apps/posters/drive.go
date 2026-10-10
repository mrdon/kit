package posters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Google Drive is where the photos live. The admin pastes a folder link
// shared as "anyone with the link: viewer"; listing goes through the Drive
// API when a Kit-wide key is set and falls back to the public folder page
// otherwise. Downloads happen in the renderer, which is the only process
// that needs bytes.

const driveFolderMime = "application/vnd.google-apps.folder"

var driveHTTP = &http.Client{Timeout: 30 * time.Second}

// DriveFile is one listed file with the path of folders above it.
type DriveFile struct {
	ID       string
	Name     string
	Folder   string // slug path of parent folders under the root, "" at root
	Modified string
	Mime     string
}

var folderIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`folders/([\w-]{10,})`),
	regexp.MustCompile(`[?&]id=([\w-]{10,})`),
	regexp.MustCompile(`^([\w-]{10,})$`),
}

// driveFolderID pulls the folder id out of a pasted link or returns the
// input when it already is one.
func driveFolderID(link string) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", nil
	}
	for _, re := range folderIDPatterns {
		if m := re.FindStringSubmatch(link); m != nil {
			return m[1], nil
		}
	}
	return "", errors.New("that does not look like a Google Drive folder link")
}

func driveDownloadURL(fileID, apiKey string) string {
	if apiKey != "" {
		return fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?alt=media&key=%s", url.PathEscape(fileID), url.QueryEscape(apiKey))
	}
	return fmt.Sprintf("https://drive.usercontent.google.com/download?id=%s&export=download&confirm=t", url.QueryEscape(fileID))
}

// driveThumbnailURL is a public thumbnail for a shared file, which the
// console can show without proxying bytes through Kit.
func driveThumbnailURL(fileID string, width int) string {
	return fmt.Sprintf("https://drive.google.com/thumbnail?id=%s&sz=w%d", url.QueryEscape(fileID), width)
}

// driveLister walks a folder tree.
type driveLister struct {
	apiKey string
}

// Walk lists every file under the folder, recursing into subfolders.
// Subfolder names become the file's Folder (slugged), which is how photos
// are grouped into sets.
func (d driveLister) Walk(ctx context.Context, folderID string) ([]DriveFile, error) {
	var out []DriveFile
	var walk func(id, rel string, depth int) error
	walk = func(id, rel string, depth int) error {
		if depth > 6 {
			return nil
		}
		files, err := d.list(ctx, id)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.Mime == driveFolderMime {
				sub := slugify(f.Name)
				if rel != "" {
					sub = rel + "/" + sub
				}
				if err := walk(f.ID, sub, depth+1); err != nil {
					return err
				}
				continue
			}
			f.Folder = rel
			out = append(out, f)
		}
		return nil
	}
	if err := walk(folderID, "", 0); err != nil {
		return nil, err
	}
	return out, nil
}

func (d driveLister) list(ctx context.Context, id string) ([]DriveFile, error) {
	if d.apiKey != "" {
		return d.listViaAPI(ctx, id)
	}
	return d.listViaPage(ctx, id)
}

func (d driveLister) listViaAPI(ctx context.Context, id string) ([]DriveFile, error) {
	var out []DriveFile
	pageToken := ""
	for {
		q := url.Values{}
		q.Set("q", fmt.Sprintf("'%s' in parents and trashed = false", id))
		q.Set("key", d.apiKey)
		q.Set("pageSize", "1000")
		q.Set("fields", "nextPageToken,files(id,name,mimeType,modifiedTime)")
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/drive/v3/files?"+q.Encode(), nil)
		res, err := driveHTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("listing Drive folder: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("the Drive API returned %d for folder %s; is it shared with anyone who has the link?", res.StatusCode, id)
		}
		var page struct {
			NextPageToken string `json:"nextPageToken"`
			Files         []struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				MimeType     string `json:"mimeType"`
				ModifiedTime string `json:"modifiedTime"`
			} `json:"files"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decoding Drive listing: %w", err)
		}
		for _, f := range page.Files {
			out = append(out, DriveFile{ID: f.ID, Name: f.Name, Mime: f.MimeType, Modified: f.ModifiedTime})
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}

// The embeddable folder view is a plain HTML list of a public folder's
// children: a link to /drive/folders/<id> or /file/d/<id> followed by a
// title. It carries no modified time, so a file changed in place keeps its
// row until it is renamed or re-uploaded.
var pageEntry = regexp.MustCompile(`href="https://drive\.google\.com/(drive/folders|file/d)/([\w-]+)[^"]*"[\s\S]*?class="flip-entry-title">([^<]+)<`)

func (d driveLister) listViaPage(ctx context.Context, id string) ([]DriveFile, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://drive.google.com/embeddedfolderview?id="+url.QueryEscape(id), nil)
	res, err := driveHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing Drive folder: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the Drive folder page returned %d for %s; is it shared with anyone who has the link?", res.StatusCode, id)
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	var out []DriveFile
	for _, m := range pageEntry.FindAllStringSubmatch(string(body), -1) {
		f := DriveFile{ID: m[2], Name: html.UnescapeString(strings.TrimSpace(m[3]))}
		if m[1] == "drive/folders" {
			f.Mime = driveFolderMime
		}
		out = append(out, f)
	}
	return out, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9.]+`)

func slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

var imageName = regexp.MustCompile(`(?i)\.(jpe?g|png|webp)$`)

// isImageFile says whether a listed file is a photo worth indexing.
func isImageFile(f DriveFile) bool {
	if strings.HasPrefix(f.Mime, "image/") {
		return !strings.Contains(f.Mime, "svg")
	}
	return imageName.MatchString(f.Name)
}
