package expense

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/auth"
	consoleweb "github.com/mrdon/kit/web/console"
)

// registerIntakeRoutes wires the PUBLIC, unauthenticated intake surface. Tenant
// comes from the {slug} path (no session), so these use TenantFromPath only —
// NOT console.JSON. The page is a clean, listed URL anyone can load; the
// approval swipe, not URL secrecy, is the control.
func registerIntakeRoutes(mux *http.ServeMux, a *ExpenseApp) {
	tenantMW := auth.TenantFromPath(a.svc.pool)
	pub := func(h http.HandlerFunc) http.Handler { return tenantMW(h) }
	mux.Handle("GET /{slug}/expenses/submit", pub(a.handleIntakePage))
	mux.Handle("POST /{slug}/api/expenses/intake/scan", pub(a.handleIntakeScan))
	mux.Handle("POST /{slug}/api/expenses/intake/submit", pub(a.handleIntakeSubmit))
}

// intakeEnabled loads the tenant policy and reports whether public intake is on.
func (a *ExpenseApp) intakeEnabled(r *http.Request, tenantID uuid.UUID) bool {
	pol, err := loadPolicy(r.Context(), a.svc.pool, tenantID)
	return err == nil && pol.IntakeEnabled && strings.TrimSpace(pol.IntakeRole) != ""
}

// handleIntakePage serves the intake SPA shell. 404 when intake is disabled so
// a disabled workspace reveals nothing.
func (a *ExpenseApp) handleIntakePage(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil || !a.intakeEnabled(r, tenant.ID) {
		http.NotFound(w, r)
		return
	}
	title := tenant.Name
	if title == "" {
		title = tenant.Slug
	}
	body, err := consoleweb.IntakeHTML(tenant.Slug, title)
	if err != nil {
		http.Error(w, "intake page not built", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// handleIntakeScan stores an uploaded receipt and returns OCR-prefilled fields
// for the submitter to correct. Public + rate-limited.
func (a *ExpenseApp) handleIntakeScan(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil || !a.intakeEnabled(r, tenant.ID) {
		http.NotFound(w, r)
		return
	}
	if !a.intakeLimiter.allow(clientIP(r)) {
		expenseErr(w, http.StatusTooManyRequests, "too many requests, please wait a moment")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, attachment.MaxBytes+(1<<20))
	if err := r.ParseMultipartForm(attachment.MaxBytes + (1 << 20)); err != nil {
		expenseErr(w, http.StatusBadRequest, "receipt upload too large or malformed")
		return
	}
	file, header, err := r.FormFile("receipt")
	if err != nil {
		expenseErr(w, http.StatusBadRequest, "a receipt file is required")
		return
	}
	defer file.Close()
	raw := make([]byte, 0, header.Size)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := file.Read(buf)
		raw = append(raw, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	mime := receiptMime(header.Header.Get("Content-Type"), raw)
	if !validReceiptMime(mime) {
		expenseErr(w, http.StatusBadRequest, "receipt must be an image or PDF")
		return
	}
	att, err := a.attachments().Store(r.Context(), tenant.ID, uuid.Nil, header.Filename, mime, raw)
	if err != nil {
		if errors.Is(err, attachment.ErrTooLarge) {
			expenseErr(w, http.StatusBadRequest, "receipt is too large (max 10 MB)")
			return
		}
		a.serviceErr(w, err)
		return
	}
	f := extractReceiptFields(r.Context(), a.llm, raw, mime)
	expenseJSON(w, http.StatusOK, map[string]any{
		"attachment_id": att.ID,
		"vendor":        f.Vendor,
		"spent_on":      f.SpentOn,
		"amount":        f.Amount,
		"tax":           f.Tax,
		"currency":      f.Currency,
	})
}

// intakeSubmitBody is the JSON the intake form posts. website is a honeypot —
// a hidden field real users leave blank.
type intakeSubmitBody struct {
	Email        string `json:"email"`
	Name         string `json:"name"`
	Purpose      string `json:"purpose"`
	Vendor       string `json:"vendor"`
	SpentOn      string `json:"spent_on"`
	Amount       string `json:"amount"`
	Tax          string `json:"tax"`
	Category     string `json:"category"`
	Currency     string `json:"currency"`
	AttachmentID string `json:"attachment_id"`
	Website      string `json:"website"` // honeypot
}

// handleIntakeSubmit creates the anonymous report from a corrected submission.
func (a *ExpenseApp) handleIntakeSubmit(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil || !a.intakeEnabled(r, tenant.ID) {
		http.NotFound(w, r)
		return
	}
	if !a.intakeLimiter.allow(clientIP(r)) {
		expenseErr(w, http.StatusTooManyRequests, "too many requests, please wait a moment")
		return
	}
	var body intakeSubmitBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	// Honeypot: a bot filled the hidden field. Pretend success, create nothing.
	if strings.TrimSpace(body.Website) != "" {
		expenseJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if !looksLikeEmail(body.Email) {
		expenseErr(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	attID, err := uuid.Parse(strings.TrimSpace(body.AttachmentID))
	if err != nil {
		expenseErr(w, http.StatusBadRequest, "please upload a receipt first")
		return
	}
	in, msg := body.toIntakeInput(attID)
	if msg != "" {
		expenseErr(w, http.StatusBadRequest, msg)
		return
	}
	rep, err := a.svc.CreateAnonymousIntake(r.Context(), tenant.ID, in)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	expenseJSON(w, http.StatusOK, map[string]any{"ok": true, "report_id": rep.ID})
}

// toIntakeInput parses the amount/date/tax fields, returning a user-facing
// message (not error) for bad input.
func (b intakeSubmitBody) toIntakeInput(attID uuid.UUID) (IntakeInput, string) {
	cents, err := parseCents(b.Amount)
	if err != nil {
		return IntakeInput{}, "amount must be a decimal like 12.34"
	}
	f := itemFields{Tax: b.Tax, SpentOn: b.SpentOn}
	tax, spentOn, _, msg := parseItemExtras(&f)
	if msg != "" {
		return IntakeInput{}, msg
	}
	in := IntakeInput{
		Email: b.Email, Name: b.Name, Purpose: b.Purpose,
		Vendor: b.Vendor, AmountCents: cents, Category: b.Category,
		Currency: b.Currency, SpentOn: spentOn, AttachmentID: &attID,
	}
	if tax != nil {
		in.TaxCents = *tax
	}
	return in, ""
}

// attachments builds the receipt store on demand (enc is wired via Configure).
func (a *ExpenseApp) attachments() *attachment.Service {
	return attachment.NewService(a.svc.pool, a.enc)
}

func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t\r\n")
}

func validReceiptMime(mime string) bool {
	return strings.HasPrefix(mime, "image/") || mime == "application/pdf"
}

// receiptMime trusts the upload's declared type, falling back to content
// sniffing when it's missing or generic.
func receiptMime(declared string, raw []byte) string {
	declared = strings.TrimSpace(strings.ToLower(declared))
	if declared != "" && declared != "application/octet-stream" {
		if i := strings.IndexByte(declared, ';'); i >= 0 {
			declared = strings.TrimSpace(declared[:i])
		}
		return declared
	}
	return strings.SplitN(http.DetectContentType(raw), ";", 2)[0]
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimiter is a tiny per-key sliding-window limiter for the public intake
// endpoints. Spam defence, not a hard quota — the approval gate is the real
// control.
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{hits: make(map[string][]time.Time), max: max, window: window}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, time.Now())
	return true
}
