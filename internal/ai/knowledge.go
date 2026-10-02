package ai

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	chunkSize      = 900     // characters per knowledge passage
	maxSourceText  = 1 << 20 // 1 MB of text per source
	maxPageBytes   = 2 << 20
	maxChunksTotal = 5000
)

var (
	scriptRE = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head|template)\b.*?</(script|style|noscript|svg|head|template)>`)
	blockRE  = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|tr|table|section|article|header|footer|main|blockquote)\b[^>]*>`)
	tagRE    = regexp.MustCompile(`(?s)<[^>]*>`)
	titleRE  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	spaceRE  = regexp.MustCompile(`[ \t\f\v\x{00a0}]+`)
	blankRE  = regexp.MustCompile(`\n{3,}`)
)

// HTMLToText returns the page title and its visible text, one paragraph per line.
func HTMLToText(src string) (title, text string) {
	if m := titleRE.FindStringSubmatch(src); m != nil {
		title = strings.TrimSpace(html.UnescapeString(tagRE.ReplaceAllString(m[1], "")))
	}
	body := scriptRE.ReplaceAllString(src, " ")
	body = blockRE.ReplaceAllString(body, "\n")
	body = tagRE.ReplaceAllString(body, " ")
	body = html.UnescapeString(body)
	return title, normalise(body)
}

func normalise(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaceRE.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(blankRE.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// Chunk splits text into passages of about chunkSize characters, breaking at paragraph and
// sentence ends where it can.
func Chunk(text string) []string {
	text = normalise(text)
	if text == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, t)
		}
		cur.Reset()
	}
	add := func(piece string) {
		if utf8.RuneCountInString(cur.String())+utf8.RuneCountInString(piece)+1 > chunkSize {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(piece)
	}
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		for utf8.RuneCountInString(para) > chunkSize {
			cut := splitPoint(para)
			add(para[:cut])
			para = strings.TrimSpace(para[cut:])
		}
		add(para)
	}
	flush()
	return out
}

// splitPoint finds where to cut a long paragraph: after the last sentence end, else the last
// space, else the hard limit. It returns a byte offset.
func splitPoint(s string) int {
	limit := 0
	for i := range s {
		if utf8.RuneCountInString(s[:i]) >= chunkSize {
			break
		}
		limit = i
	}
	head := s[:limit]
	for _, sep := range []string{". ", "? ", "! ", "। ", "; ", ", ", " "} {
		if i := strings.LastIndex(head, sep); i > len(head)/3 {
			return i + len(sep)
		}
	}
	if limit == 0 {
		return len(s)
	}
	return limit
}

// stopWords are dropped from search queries; English only, other languages keep every word.
var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be by can could do does for from has have how i if in is it its me my of on or our so than that the their them there these they this to us was we were what when where which who why will with would you your please hi hello thanks`) {
		stopWords[w] = true
	}
}

// Words lists the distinct lower-case words of a question that are worth searching for.
func Words(q string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r))
	}) {
		if utf8.RuneCountInString(w) < 2 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) == 16 {
			break
		}
	}
	return out
}

// TSQuery turns words into a to_tsquery string that matches any of them.
func TSQuery(words []string) string { return strings.Join(words, " | ") }

// Score counts how many of the words a passage contains, weighting longer words more.
func Score(content string, words []string) float64 {
	c := strings.ToLower(content)
	var s float64
	for _, w := range words {
		if strings.Contains(c, w) {
			s += 1 + float64(utf8.RuneCountInString(w))/10
		}
	}
	return s
}

// Fetcher downloads web pages for the knowledge base. It refuses addresses inside the network,
// at connection time, so a DNS answer or a redirect cannot reach them.
type Fetcher struct {
	// AllowPrivate lets tests fetch from a local server.
	AllowPrivate bool
	Timeout      time.Duration
}

var errBlockedAddress = errors.New("that address is not allowed")

func blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if cgnat := netip.MustParsePrefix("100.64.0.0/10"); cgnat.Contains(ip) {
		return true
	}
	return false
}

// CheckURL validates a page address a user typed.
func CheckURL(raw string) (*url.URL, error) { return checkURL(raw, false) }

func checkURL(raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("enter a web address starting with https://")
	}
	if allowPrivate {
		return u, nil
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return nil, fmt.Errorf("only ports 80 and 443 are allowed")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && blocked(ip) {
		return nil, errBlockedAddress
	}
	return u, nil
}

func (f Fetcher) client() *http.Client {
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !f.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || blocked(ip) {
				return errBlockedAddress
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext, MaxIdleConns: 1, DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			_, err := checkURL(req.URL.String(), f.AllowPrivate)
			return err
		},
	}
}

// Page fetches a web page and returns its title and text.
func (f Fetcher) Page(ctx context.Context, raw string) (title, text string, err error) {
	u, err := checkURL(raw, f.AllowPrivate)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "EcogoBot/1.0 (+https://ecogo.co.in)")
	req.Header.Set("Accept", "text/html,text/plain;q=0.9")
	resp, err := f.client().Do(req)
	if err != nil {
		if errors.Is(err, errBlockedAddress) {
			return "", "", errBlockedAddress
		}
		return "", "", fmt.Errorf("the page could not be downloaded")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("the page answered with status %d", resp.StatusCode)
	}
	raw2, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return "", "", fmt.Errorf("the page could not be read")
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "html") || ct == "":
		title, text = HTMLToText(string(raw2))
	case strings.HasPrefix(ct, "text/"):
		text = normalise(string(raw2))
	default:
		return "", "", fmt.Errorf("only web pages and plain text can be read, not %s", ct)
	}
	if utf8.RuneCountInString(text) < 20 {
		return "", "", fmt.Errorf("the page has no readable text (pages built with JavaScript may not work)")
	}
	return title, text, nil
}
