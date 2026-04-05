package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	readability "codeberg.org/readeck/go-readability"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/redis/go-redis/v9"
)

const (
	maxResponseSize  = 10 * 1024 * 1024 // 10 MB
	fetchTimeout     = 10 * time.Second
	cacheTTL         = 1 * time.Hour
	maxSectionTokens = 500
	maxRedirects     = 5
)

// Page is the cached representation of a fetched web page, split by sections.
type Page struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Sections []Section `json:"sections"`
}

// Section is a heading + its content.
type Section struct {
	Heading string `json:"heading"`
	Content string `json:"content"`
	Level   int    `json:"level"` // 1-6 for h1-h6, 0 for preamble
}

// Outline returns just headings for the overview response.
func (p *Page) Outline() string {
	var b strings.Builder
	b.WriteString("*" + p.Title + "*\n" + p.URL + "\n\n")
	for _, s := range p.Sections {
		indent := ""
		if s.Level > 1 {
			indent = strings.Repeat("  ", s.Level-1)
		}
		heading := s.Heading
		if heading == "" {
			heading = "(intro)"
		}
		b.WriteString(indent + "• " + heading + "\n")
	}
	return b.String()
}

// FindSections returns sections matching a heading name (case-insensitive partial match).
func (p *Page) FindSections(heading string) string {
	heading = strings.ToLower(heading)
	var b strings.Builder
	for _, s := range p.Sections {
		if strings.Contains(strings.ToLower(s.Heading), heading) {
			writeSection(&b, &s)
		}
	}
	if b.Len() == 0 {
		return "No section matching '" + heading + "' found."
	}
	return b.String()
}

// SearchContent returns sections containing the query text (case-insensitive).
func (p *Page) SearchContent(query string) string {
	query = strings.ToLower(query)
	var b strings.Builder
	for _, s := range p.Sections {
		if strings.Contains(strings.ToLower(s.Content), query) ||
			strings.Contains(strings.ToLower(s.Heading), query) {
			writeSection(&b, &s)
		}
	}
	if b.Len() == 0 {
		return "No content matching '" + query + "' found."
	}
	return b.String()
}

func writeSection(b *strings.Builder, s *Section) {
	if s.Heading != "" {
		b.WriteString("*" + s.Heading + "*\n")
	}
	content := s.Content
	// Rough truncation at ~maxSectionTokens (estimate 4 chars per token)
	if len(content) > maxSectionTokens*4 {
		content = content[:maxSectionTokens*4]
		if idx := strings.LastIndex(content, ". "); idx > 0 {
			content = content[:idx+1]
		}
		content += "\n[truncated]"
	}
	b.WriteString(content + "\n\n")
}

// Fetcher handles web page fetching, extraction, and caching.
type Fetcher struct {
	client *http.Client
	redis  *redis.Client
}

// NewFetcher creates a new web fetcher with SSRF protection.
func NewFetcher(rdb *redis.Client) *Fetcher {
	dialer := &net.Dialer{
		Timeout: fetchTimeout,
		Control: ssrfControl,
	}

	transport := &http.Transport{
		DialContext: dialer.DialContext,
	}

	client := &http.Client{
		Timeout:   fetchTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("too many redirects")
			}
			return validateURL(req.URL)
		},
	}

	return &Fetcher{client: client, redis: rdb}
}

// Fetch retrieves a page, caches it, and returns the structured content.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if err := validateURL(u); err != nil {
		return nil, err
	}

	// Check cache
	cacheKey := "webfetch:" + hashURL(rawURL)
	if f.redis != nil {
		cached, err := f.redis.Get(ctx, cacheKey).Bytes()
		if err == nil {
			var page Page
			if json.Unmarshal(cached, &page) == nil {
				return &page, nil
			}
		}
	}

	// Fetch
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Kit/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// Extract with readability
	article, err := readability.FromReader(strings.NewReader(string(body)), u)
	if err != nil {
		return nil, fmt.Errorf("extracting content: %w", err)
	}

	// Convert to markdown
	mdContent, err := htmltomarkdown.ConvertString(article.Content)
	if err != nil {
		// Fallback to text content
		mdContent = article.TextContent
	}

	// Split into sections by headings
	page := &Page{
		URL:      rawURL,
		Title:    article.Title,
		Sections: splitSections(mdContent),
	}

	// Cache
	if f.redis != nil {
		data, _ := json.Marshal(page)
		f.redis.Set(ctx, cacheKey, data, cacheTTL)
	}

	return page, nil
}

// splitSections splits markdown content by headings into sections.
func splitSections(md string) []Section {
	lines := strings.Split(md, "\n")
	var sections []Section
	var current Section
	var contentLines []string

	for _, line := range lines {
		heading, level := parseHeading(line)
		if level > 0 {
			// Save previous section
			current.Content = strings.TrimSpace(strings.Join(contentLines, "\n"))
			if current.Content != "" || current.Heading != "" {
				sections = append(sections, current)
			}
			current = Section{Heading: heading, Level: level}
			contentLines = nil
		} else {
			contentLines = append(contentLines, line)
		}
	}

	// Save last section
	current.Content = strings.TrimSpace(strings.Join(contentLines, "\n"))
	if current.Content != "" || current.Heading != "" {
		sections = append(sections, current)
	}

	return sections
}

func parseHeading(line string) (string, int) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for _, c := range trimmed {
		if c == '#' {
			level++
		} else {
			break
		}
	}
	if level > 0 && level <= 6 && len(trimmed) > level && trimmed[level] == ' ' {
		return strings.TrimSpace(trimmed[level+1:]), level
	}
	return "", 0
}

func validateURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return errors.New("private/internal IPs not allowed")
	}
	return nil
}

func ssrfControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return errors.New("SSRF blocked: private/internal IP")
	}
	return nil
}

func hashURL(rawURL string) string {
	h := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(h[:8])
}
