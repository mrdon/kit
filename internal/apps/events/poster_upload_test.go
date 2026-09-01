package events

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/crypto"
)

// testRedis connects to the compose Redis (`make up`), matching what
// testdb.Open does for Postgres: these tests exercise real GETDEL semantics
// rather than a stand-in, because "single use" is exactly the property a
// stand-in would be most likely to get wrong.
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:6389/1"
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parsing REDIS_URL: %v", err)
	}
	c := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis unreachable (run `make up`): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// newUploadFixture is the sync fixture with the two things poster uploads
// need wired the way main.go wires them: a real Redis for the grants and an
// encryptor for the attachment bytes.
func newUploadFixture(t *testing.T) *syncFixture {
	t.Helper()
	sf := newSyncFixture(t)
	enc, err := crypto.NewEncryptor(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	sf.app.enc = enc
	sf.app.redis = testRedis(t)
	sf.app.baseURL = "https://kit.example.com"
	return sf
}

// The whole promise of the link is that it works once. Two redemptions of the
// same token must not both find a grant -- and because GETDEL is atomic, the
// loser sees redis.Nil rather than a partially-consumed grant.
func TestPosterUploadTokenIsSingleUse(t *testing.T) {
	f := newUploadFixture(t)
	e := f.create(t, CreateParams{Title: "Bike Night"})
	grant := posterGrant{TenantID: f.tenant.ID, UserID: uuid.New(), EventID: e.ID}

	token, err := f.app.mintPosterUpload(f.ctx, grant)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	got, err := f.app.redeemPosterUpload(f.ctx, token)
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if got.EventID != e.ID || got.TenantID != f.tenant.ID || got.UserID != grant.UserID {
		t.Fatalf("grant round-trip lost data: %+v", got)
	}

	if _, err := f.app.redeemPosterUpload(f.ctx, token); !errors.Is(err, redis.Nil) {
		t.Fatalf("second redeem err = %v, want redis.Nil — the link was reusable", err)
	}
}

// A token nobody has ever minted must be indistinguishable from a spent one.
func TestPosterUploadUnknownTokenIsNotFound(t *testing.T) {
	f := newUploadFixture(t)
	if _, err := f.app.redeemPosterUpload(f.ctx, "not-a-real-token"); !errors.Is(err, redis.Nil) {
		t.Fatalf("unknown token err = %v, want redis.Nil", err)
	}
	if _, err := f.app.redeemPosterUpload(f.ctx, ""); !errors.Is(err, redis.Nil) {
		t.Fatalf("empty token err = %v, want redis.Nil", err)
	}
}

// The grant must actually expire on its own. Checking the key's TTL is the
// honest version of this test -- sleeping 15 minutes is not an option, and
// asserting on the constant alone would pass even if the TTL were never set.
func TestPosterUploadTokenExpires(t *testing.T) {
	f := newUploadFixture(t)
	e := f.create(t, CreateParams{Title: "Bike Night"})
	token, err := f.app.mintPosterUpload(f.ctx,
		posterGrant{TenantID: f.tenant.ID, UserID: uuid.New(), EventID: e.ID})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	ttl, err := f.app.redis.TTL(f.ctx, posterUploadKeyPrefix+token).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > posterUploadTTL {
		t.Fatalf("TTL = %v, want (0, %v]", ttl, posterUploadTTL)
	}
	_, _ = f.app.redeemPosterUpload(f.ctx, token)
}

// A grant names one event. Presenting a valid token at another event's URL
// must be refused -- otherwise the link would be a workspace-wide poster
// write, not the narrow grant it is documented to be.
func TestPosterUploadRejectsWrongEvent(t *testing.T) {
	f := newUploadFixture(t)
	mine := f.create(t, CreateParams{Title: "Bike Night"})
	other := f.create(t, CreateParams{Title: "Quiz Night"})

	token, err := f.app.mintPosterUpload(f.ctx,
		posterGrant{TenantID: f.tenant.ID, UserID: uuid.New(), EventID: mine.ID})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	rec := f.postPoster(t, other.ID, token, jpegBytes())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	// And the token is spent regardless, so it cannot be retried at the right
	// event after being caught pointing at the wrong one.
	if _, err := f.app.redeemPosterUpload(f.ctx, token); !errors.Is(err, redis.Nil) {
		t.Fatal("a rejected upload left the token redeemable")
	}
}

// The end-to-end path: mint, POST an image, and the event comes back with a
// poster on it. This is the behaviour the whole file exists to deliver.
func TestPosterUploadAttachesImage(t *testing.T) {
	f := newUploadFixture(t)
	e := f.create(t, CreateParams{Title: "Bike Night"})

	token, err := f.app.mintPosterUpload(f.ctx,
		posterGrant{TenantID: f.tenant.ID, UserID: uuid.Nil, EventID: e.ID})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	rec := f.postPoster(t, e.ID, token, jpegBytes())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	got, err := f.svc.Get(f.ctx, f.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HeroAttachmentID == nil {
		t.Fatal("event has no poster after a successful upload")
	}
}

// The allowlist is enforced on this route too, not only the console one --
// the extract exists so the two doors cannot diverge. An SVG through here
// would be stored XSS just the same.
func TestPosterUploadRejectsScriptableImage(t *testing.T) {
	f := newUploadFixture(t)
	e := f.create(t, CreateParams{Title: "Bike Night"})

	token, err := f.app.mintPosterUpload(f.ctx,
		posterGrant{TenantID: f.tenant.ID, UserID: uuid.Nil, EventID: e.ID})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	rec := f.postPoster(t, e.ID, token, svg)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}

	got, err := f.svc.Get(f.ctx, f.tenant.ID, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HeroAttachmentID != nil {
		t.Fatal("a rejected image was still attached to the event")
	}
}

// Without Redis there is nowhere to keep a grant, so minting must fail loudly
// rather than hand back a link that dies on redemption.
func TestPosterUploadNeedsRedis(t *testing.T) {
	a := &App{baseURL: "https://kit.example.com"}
	if _, err := a.mintPosterUpload(context.Background(), posterGrant{}); !errors.Is(err, errUploadsUnconfigured) {
		t.Fatalf("mint without redis err = %v, want errUploadsUnconfigured", err)
	}
	// And the create-time offer swallows it: a link that cannot be minted must
	// never turn a successful create into a failure.
	if got := a.posterUploadOffer(context.Background(), uuid.New(), uuid.New(), uuid.New()); got != "" {
		t.Fatalf("offer without redis = %q, want empty", got)
	}
}

// PosterUploadURL's shape is what a caller pastes into curl, so pin it --
// including that it tolerates a trailing slash on the base and escapes the
// token (base64url needs no escaping, but the URL must not depend on that).
func TestPosterUploadURLShape(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	want := "https://kit.example.com/gravity/api/events/" + id.String() + "/poster/upload?t=abc123"
	for _, base := range []string{"https://kit.example.com", "https://kit.example.com/"} {
		if got := PosterUploadURL(base, "gravity", id, "abc123"); got != want {
			t.Errorf("PosterUploadURL(%q) = %q, want %q", base, got, want)
		}
	}
	if got := PosterUploadURL("https://k", "t", id, "a+b/c"); !strings.Contains(got, "t=a%2Bb%2Fc") {
		t.Errorf("token not escaped: %q", got)
	}
}

// postPoster drives the real handler through the tenant middleware, so the
// tenant-from-path resolution the grant is checked against is the one under
// test rather than a value the test set by hand.
func (f *syncFixture) postPoster(t *testing.T, eventID uuid.UUID, token string, image []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartPoster(t, "poster.jpg", image)
	target := "/" + f.tenant.Slug + "/api/events/" + eventID.String() + "/poster/upload?t=" + token
	req := httptest.NewRequest(http.MethodPost, target, body)
	req.Header.Set("Content-Type", contentType)

	mux := http.NewServeMux()
	registerPosterUploadRoutes(muxAdapter{mux}, f.app)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// multipartPoster builds the form body the handler parses, under the field
// name the production form uses.
func multipartPoster(t *testing.T, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("poster", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing writer: %v", err)
	}
	return &body, w.FormDataContentType()
}

// jpegBytes is the smallest thing http.DetectContentType calls a JPEG. The
// handler sniffs rather than trusting the filename, so the bytes have to be
// real even though nothing ever decodes them.
func jpegBytes() []byte {
	return append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 512)...)
}
