package robotspolicy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAllowsBlocksAndCrawlDelay verifies allow, disallow and Crawl-delay handling.
func TestAllowsBlocksAndCrawlDelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprintln(w, "User-agent: *")
			fmt.Fprintln(w, "Disallow: /private")
			fmt.Fprintln(w, "Crawl-delay: 1")
			return
		}
		fmt.Fprintln(w, "ok")
	}))
	defer srv.Close()

	p := New("goCrawler-test", 2*time.Second)
	if !p.Allowed(srv.URL + "/public/page") {
		t.Fatal("public page should be allowed")
	}
	if p.Allowed(srv.URL + "/private/page") {
		t.Fatal("disallowed page should be blocked")
	}

	start := time.Now()
	p.Wait(srv.URL + "/public/page")
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("first Wait should not sleep")
	}
	p.Wait(srv.URL + "/public/other")
	if time.Since(start) < 900*time.Millisecond {
		t.Fatal("Crawl-delay was not honoured")
	}
}

// TestMissingRobotsFailsOpen verifies that a missing robots.txt allows crawling.
func TestMissingRobotsFailsOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := New("goCrawler-test", 2*time.Second)
	if !p.Allowed(srv.URL + "/anything") {
		t.Fatal("missing robots.txt should allow crawling")
	}
}

func TestDiskCachePersistsAcrossInstances(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			requests++
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintln(w, "User-agent: *")
			fmt.Fprintln(w, "Disallow: /private")
			fmt.Fprintln(w, "Crawl-delay: 1")
			return
		}
		fmt.Fprintln(w, "ok")
	}))
	defer server.Close()

	cacheFile := filepath.Join(t.TempDir(), "robots-cache.json")
	p1 := NewWithCache("goCrawler-test", time.Second, cacheFile)
	if !p1.Allowed(server.URL + "/public") {
		t.Fatal("public page should be allowed")
	}
	if requests != 1 {
		t.Fatalf("first instance made %d robots requests, want 1", requests)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("cache file was not written: %v", err)
	}

	p2 := NewWithCache("goCrawler-test", time.Second, cacheFile)
	if p2.Allowed(server.URL + "/private") {
		t.Fatal("disallowed page should be blocked from cached rules")
	}
	if requests != 1 {
		t.Fatalf("cache was not reused: %d robots requests", requests)
	}
}

func TestDiskCacheExpiresAfterSevenDays(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			requests++
			fmt.Fprintln(w, "User-agent: *")
			fmt.Fprintln(w, "Disallow: /old")
			return
		}
		fmt.Fprintln(w, "ok")
	}))
	defer server.Close()

	cacheFile := filepath.Join(t.TempDir(), "robots-cache.json")
	hostKey, _, err := hostKeyOf(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	stale := robotsCacheFile{
		Version: robotsCacheVersion,
		Hosts: map[string]cachedRobots{
			hostKey: {
				Body:      "User-agent: *\nDisallow: /stale\n",
				FetchedAt: time.Now().Add(-8 * 24 * time.Hour),
			},
		},
	}
	b, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, b, 0644); err != nil {
		t.Fatal(err)
	}

	p := NewWithCache("goCrawler-test", time.Second, cacheFile)
	p.Allowed(server.URL + "/old")
	if requests != 1 {
		t.Fatalf("stale cache was used: %d robots requests", requests)
	}
	if p.Allowed(server.URL + "/old") {
		t.Fatal("fresh rules should disallow /old")
	}
	if requests != 1 {
		t.Fatalf("fresh cache was not reused: %d robots requests", requests)
	}
}
