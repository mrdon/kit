package events

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// minimalPDF is enough for content sniffing, which is all the upload checks.
var minimalPDF = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")

// The path the feature exists for: upload under a title, upload again under
// the same title in another case, and there is still one topper -- same id,
// new file, the old file's bytes gone -- until it is removed.
func TestTopperLibraryUploadReplaceRemove(t *testing.T) {
	f := newUploadFixture(t)

	first := f.postTopper(t, "Happy hour", minimalPDF)
	if first.Code != http.StatusOK {
		t.Fatalf("first upload status = %d; body = %s", first.Code, first.Body.String())
	}
	before := f.onlyTopper(t)

	second := f.postTopper(t, "happy HOUR", append([]byte{}, minimalPDF...))
	if second.Code != http.StatusOK {
		t.Fatalf("replace status = %d; body = %s", second.Code, second.Body.String())
	}
	after := f.onlyTopper(t)
	if after.ID != before.ID {
		t.Fatalf("replacing changed the topper id: %s -> %s", before.ID, after.ID)
	}
	if after.AttachmentID == before.AttachmentID {
		t.Fatal("replacing kept the old file")
	}
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM attachments WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, before.AttachmentID).Scan(&n); err != nil {
		t.Fatalf("counting old file: %v", err)
	}
	if n != 0 {
		t.Fatal("the replaced file was left behind")
	}

	title, err := deleteLibraryTopper(f.ctx, f.pool, f.tenant.ID, after.ID)
	if err != nil || title != "happy HOUR" {
		t.Fatalf("delete = %q, %v", title, err)
	}
	if got, _ := listLibraryToppers(f.ctx, f.pool, f.tenant.ID); len(got) != 0 {
		t.Fatalf("library after delete = %v, want empty", got)
	}
}

// What gets printed is exactly what was stored, so anything that is not a PDF
// is turned away rather than filed.
func TestTopperLibraryRejectsNonPDF(t *testing.T) {
	f := newUploadFixture(t)
	rec := f.postTopper(t, "Happy hour", jpegBytes())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if got, _ := listLibraryToppers(f.ctx, f.pool, f.tenant.ID); len(got) != 0 {
		t.Fatalf("a rejected file was filed: %v", got)
	}
}

func (f *syncFixture) onlyTopper(t *testing.T) LibraryTopper {
	t.Helper()
	got, err := listLibraryToppers(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("library has %d toppers, want 1", len(got))
	}
	return got[0]
}

func (f *syncFixture) postTopper(t *testing.T, title string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	token, err := f.app.mintUploadGrant(f.ctx, topperUploadKeyPrefix,
		topperGrant{TenantID: f.tenant.ID, UserID: uuid.Nil, Title: title})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("pdf", "topper.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing form: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost,
		"/"+f.tenant.Slug+"/api/events/topper-library/upload?t="+token, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	mux := http.NewServeMux()
	registerTopperLibraryRoutes(muxAdapter{mux}, f.app)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
