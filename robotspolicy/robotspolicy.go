// Package robotspolicy fetches, caches and enforces robots.txt rules.
// The cache is kept in memory and persisted to disk with a seven-day TTL.
package robotspolicy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

const (
	robotsCacheVersion = 1
	robotsCacheTTL     = 7 * 24 * time.Hour
	defaultRobotsCache = "./cache/robots-cache.json"
)

type cachedRobots struct {
	Body      string    `json:"body"`
	FetchedAt time.Time `json:"fetched_at"`
}

type robotsCacheFile struct {
	Version int                     `json:"version"`
	Hosts   map[string]cachedRobots `json:"hosts"`
}

type hostRules struct {
	group     *robotstxt.Group
	delay     time.Duration
	body      string
	fetchedAt time.Time
}

// Policy holds one cached robots.txt per host plus the last request time used
// to honour Crawl-delay.
type Policy struct {
	mu        sync.Mutex
	client    *http.Client
	userAgent string
	rules     map[string]*hostRules
	lastReq   map[string]time.Time
	cachePath string
}

// New creates a policy using the default on-disk cache location.
func New(userAgent string, timeout time.Duration) *Policy {
	return NewWithCache(userAgent, timeout, defaultRobotsCache)
}

// NewWithCache creates a policy with an explicit on-disk cache path.
// An empty path disables disk persistence.
func NewWithCache(userAgent string, timeout time.Duration, cachePath string) *Policy {
	p := &Policy{
		client:    &http.Client{Timeout: timeout},
		userAgent: userAgent,
		rules:     make(map[string]*hostRules),
		lastReq:   make(map[string]time.Time),
		cachePath: cachePath,
	}
	p.loadCache()
	return p
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

// loadCache restores non-expired robots.txt documents from disk.
func (p *Policy) loadCache() {
	if p.cachePath == "" {
		return
	}
	b, err := os.ReadFile(p.cachePath)
	if err != nil {
		return
	}
	var saved robotsCacheFile
	if err := json.Unmarshal(b, &saved); err != nil || saved.Version != robotsCacheVersion {
		return
	}
	now := time.Now()
	for host, entry := range saved.Hosts {
		if now.Sub(entry.FetchedAt) >= robotsCacheTTL || entry.Body == "" {
			continue
		}
		data, err := robotstxt.FromBytes([]byte(entry.Body))
		if err != nil {
			continue
		}
		r := &hostRules{body: entry.Body, fetchedAt: entry.FetchedAt}
		r.group = data.FindGroup(p.userAgent)
		if r.group != nil && r.group.CrawlDelay > 0 {
			r.delay = r.group.CrawlDelay
		}
		p.rules[host] = r
	}
}

// saveCacheLocked writes the current rules to disk. The caller must hold p.mu.
func (p *Policy) saveCacheLocked() {
	if p.cachePath == "" {
		return
	}
	saved := robotsCacheFile{
		Version: robotsCacheVersion,
		Hosts:   make(map[string]cachedRobots),
	}
	for host, rules := range p.rules {
		if rules.body == "" {
			continue
		}
		saved.Hosts[host] = cachedRobots{Body: rules.body, FetchedAt: rules.fetchedAt}
	}
	b, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p.cachePath), 0755); err != nil {
		return
	}
	tmp := p.cachePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return
	}
	os.Rename(tmp, p.cachePath)
}

// rulesFor fetches, parses and caches the robots.txt rules of a host.
func (p *Policy) rulesFor(hostKey string) *hostRules {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cached, ok := p.rules[hostKey]; ok {
		if p.rulesFresh(cached) {
			return cached
		}
	}

	req, err := http.NewRequest("GET", hostKey+"/robots.txt", nil)
	if err != nil {
		return &hostRules{}
	}
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		fmt.Printf(" [ROBOTS fetch error: %v]", err)
		return &hostRules{}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return &hostRules{}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &hostRules{}
	}
	data, err := robotstxt.FromBytes(body)
	if err != nil {
		return &hostRules{}
	}

	r := &hostRules{body: string(body), fetchedAt: time.Now()}
	r.group = data.FindGroup(p.userAgent)
	if r.group != nil && r.group.CrawlDelay > 0 {
		r.delay = r.group.CrawlDelay
	}
	p.rules[hostKey] = r
	p.saveCacheLocked()
	return r
}

// rulesFresh reports whether an in-memory entry is still within the TTL.
func (p *Policy) rulesFresh(r *hostRules) bool {
	return r.body != "" && time.Since(r.fetchedAt) < robotsCacheTTL
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
	now := time.Now()
	next := now
	if last, ok := p.lastReq[hostKey]; ok && last.After(now) {
		next = last
	}
	p.lastReq[hostKey] = next.Add(r.delay)
	p.mu.Unlock()

	if sleep := time.Until(next); sleep > 0 {
		time.Sleep(sleep)
	}
}
