package robotspolicy_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"goCrawler/robotspolicy"
)

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

	p := robotspolicy.New("goCrawler-test", 2*time.Second)
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

func TestMissingRobotsFailsOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := robotspolicy.New("goCrawler-test", 2*time.Second)
	if !p.Allowed(srv.URL + "/anything") {
		t.Fatal("missing robots.txt should allow crawling")
	}
}
