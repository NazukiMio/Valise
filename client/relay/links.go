// Package relay finds login links in a login command's terminal output and
// works out the loopback callback a browser will be sent to
// (docs/LOGIN_RELAY.md sections 3 and 5.2).
package relay

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)
	linkRe = regexp.MustCompile(`https?://[^\s"'<>\x60│┃|]+`)
)

// StripANSI removes terminal control sequences.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// Scanner collects links from a byte stream that arrives in pieces. Only
// complete lines are scanned, so a link split across reads is not lost.
type Scanner struct {
	pattern *regexp.Regexp
	mu      sync.Mutex
	partial string
	seen    map[string]bool
	links   []string
}

// NewScanner uses pattern (a profile url_pattern; first group if any) when
// non-empty, else any http(s) link.
func NewScanner(pattern string) (*Scanner, error) {
	s := &Scanner{seen: map[string]bool{}}
	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		s.pattern = re
	}
	return s, nil
}

// Write feeds output; it never fails.
func (s *Scanner) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := s.partial + string(b)
	i := strings.LastIndexAny(text, "\r\n")
	if i < 0 {
		s.partial = text
		if len(s.partial) > 64<<10 {
			s.partial = s.partial[len(s.partial)-64<<10:]
		}
		return len(b), nil
	}
	s.partial = text[i+1:]
	s.scan(text[:i+1])
	return len(b), nil
}

// Flush scans whatever is left without a line end.
func (s *Scanner) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scan(s.partial)
	s.partial = ""
}

func (s *Scanner) scan(text string) {
	for _, line := range strings.FieldsFunc(StripANSI(text), func(r rune) bool { return r == '\n' || r == '\r' }) {
		for _, l := range s.find(line) {
			l = strings.TrimRight(l, ".,;:)]}>")
			if _, err := url.Parse(l); err != nil || s.seen[l] {
				continue
			}
			s.seen[l] = true
			s.links = append(s.links, l)
		}
	}
}

func (s *Scanner) find(line string) []string {
	if s.pattern == nil {
		return linkRe.FindAllString(line, -1)
	}
	var out []string
	for _, m := range s.pattern.FindAllStringSubmatch(line, -1) {
		if len(m) > 1 {
			out = append(out, m[1])
		} else {
			out = append(out, m[0])
		}
	}
	return out
}

// Links returns the links found so far, in order.
func (s *Scanner) Links() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.links...)
}

// Domain is the host a link points at, shown before opening it.
func Domain(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Callback is the loopback address a login flow redirects the browser to.
type Callback struct {
	Host string // as written: localhost, 127.0.0.1 or ::1
	Port int
}

// LoopbackCallback finds the loopback redirect of an authorization link:
// a query value (redirect_uri and the like) that is a loopback URL, or the
// link itself.
func LoopbackCallback(link string) (Callback, bool) {
	u, err := url.Parse(link)
	if err != nil {
		return Callback{}, false
	}
	if cb, ok := loopback(u); ok {
		return cb, true
	}
	for _, key := range []string{"redirect_uri", "redirect_url", "redirect", "callback", "callback_url", "return_to"} {
		if v := u.Query().Get(key); v != "" {
			if r, err := url.Parse(v); err == nil {
				if cb, ok := loopback(r); ok {
					return cb, true
				}
			}
		}
	}
	for _, vs := range u.Query() {
		for _, v := range vs {
			if r, err := url.Parse(v); err == nil && r.Scheme != "" {
				if cb, ok := loopback(r); ok {
					return cb, true
				}
			}
		}
	}
	return Callback{}, false
}

func loopback(u *url.URL) (Callback, bool) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return Callback{}, false
	}
	host := u.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return Callback{}, false
		}
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return Callback{}, false
		}
		port = n
	}
	return Callback{Host: host, Port: port}, true
}
