// Package robotspolicy fetches, caches and enforces robots.txt rules.
// Missing or unreachable robots.txt files are treated as "allow" (fail open),
// matching the behaviour of most crawlers.
package robotspolicy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

type hostRules struct {
	group *robotstxt.Group
	delay time.Duration
}

// Policy holds one cached robots.txt per host plus the last request time used
// to honour Crawl-delay.
type Policy struct {
	mu        sync.Mutex
	client    *http.Client
	userAgent string
	rules     map[string]*hostRules
	lastReq   map[string]time.Time
}

// New creates a policy with an HTTP client and empty per-host caches.
func New(userAgent string, timeout time.Duration) *Policy {
	return &Policy{
		client:    &http.Client{Timeout: timeout},
		userAgent: userAgent,
		rules:     make(map[string]*hostRules),
		lastReq:   make(map[string]time.Time),
	}
}

// hostKeyOf splits a URL into its host key and request path.
func hostKeyOf(rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("robots: URL without scheme or host: %q", rawURL)
	}
	return u.Scheme + "://" + u.Host, u.RequestURI(), nil
}

// rulesFor fetches, parses and caches the robots.txt rules of a host.
func (p *Policy) rulesFor(hostKey string) *hostRules {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cached, ok := p.rules[hostKey]; ok {
		return cached
	}

	r := &hostRules{}
	p.rules[hostKey] = r

	req, err := http.NewRequest("GET", hostKey+"/robots.txt", nil)
	if err != nil {
		return r
	}
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		fmt.Printf(" [ROBOTS fetch error: %v]", err)
		return r
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return r
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return r
	}
	data, err := robotstxt.FromBytes(body)
	if err != nil {
		return r
	}
	r.group = data.FindGroup(p.userAgent)
	if r.group != nil && r.group.CrawlDelay > 0 {
		r.delay = r.group.CrawlDelay
	}
	return r
}

// Allowed reports whether robots.txt permits fetching rawURL.
func (p *Policy) Allowed(rawURL string) bool {
	hostKey, path, err := hostKeyOf(rawURL)
	if err != nil {
		return true
	}
	r := p.rulesFor(hostKey)
	if r.group == nil {
		return true
	}
	return r.group.Test(path)
}

// Wait sleeps the Crawl-delay of the host since its last request, if any.
func (p *Policy) Wait(rawURL string) {
	hostKey, _, err := hostKeyOf(rawURL)
	if err != nil {
		return
	}
	r := p.rulesFor(hostKey)
	if r.delay <= 0 {
		return
	}

	p.mu.Lock()
	var sleep time.Duration
	if last, ok := p.lastReq[hostKey]; ok {
		sleep = r.delay - time.Since(last)
	}
	p.lastReq[hostKey] = time.Now()
	p.mu.Unlock()

	if sleep > 0 {
		time.Sleep(sleep)
	}
}
