package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"goCrawler/storelib"
)

func withApp(t *testing.T, fn func()) {
	t.Helper()
	old := app
	defer func() { app = old }()
	app = newCrawler()
	setupTestConfig()
	app.MinDocLen = 50
	app.MaxDocLen = 100000
	app.MaxLinksPerPage = 200
	app.CacheBackend = storelib.BackendLocal
	app.CachePath = "./cache/cache.db"
	app.CLIWorkers = 1
	fn()
}

func TestValidateConfig(t *testing.T) {
	withApp(t, func() {
		if err := validateConfig(); err != nil {
			t.Fatalf("valid config rejected: %v", err)
		}
		app.MinDocLen = 100
		app.MaxDocLen = 50
		app.RankingStrategy = "nope"
		app.CacheBackend = "nope"
		err := validateConfig()
		if err == nil {
			t.Fatal("invalid config accepted")
		}
		for _, fragment := range []string{"document length", "rankingStrategy", "cacheBackend"} {
			if !strings.Contains(err.Error(), fragment) {
				t.Fatalf("error %q missing %q", err, fragment)
			}
		}
	})
}

func TestDownloadHTTPStatusAndContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/notfound":
			http.NotFound(w, r)
		case "/binary":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-1.4"))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body><p>stack image noise signal siril stacking</p></body></html>"))
		}
	}))
	defer server.Close()

	withApp(t, func() {
		withTempWorkDir(t, func() {
			if err := initFileLoggers(); err != nil {
				t.Fatal(err)
			}
			defer closeFileLoggers()
			app.DownloadTimeout = 2 * time.Second
			app.HTTPClient = &http.Client{Timeout: 2 * time.Second}
			app.FailedDomains = make(map[string]int)

			if _, _, err := download(server.URL + "/notfound"); err == nil {
				t.Fatal("404 should fail")
			}
			if _, _, err := download(server.URL + "/binary"); err == nil {
				t.Fatal("binary content should fail")
			}
			content, links, err := download(server.URL + "/ok")
			if err != nil || !strings.Contains(content, "stack") || len(links) != 0 {
				t.Fatalf("download ok: content=%q links=%v err=%v", content, links, err)
			}
		})
	})
}

func TestDownloadCachedCorruptEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("fresh content"))
	}))
	defer server.Close()

	withApp(t, func() {
		withTempWorkDir(t, func() {
			if err := initFileLoggers(); err != nil {
				t.Fatal(err)
			}
			defer closeFileLoggers()
			app.CacheStore = &memoryStore{data: map[string]string{"cache_" + server.URL: "{not json"}}
			app.HTTPClient = &http.Client{Timeout: 2 * time.Second}
			app.DownloadTimeout = 2 * time.Second

			content, _, err := downloadCached(server.URL)
			if err != nil || content != "fresh content" {
				t.Fatalf("corrupt cache was not recovered: %q %v", content, err)
			}
		})
	})
}

func TestRespectCrawlDelay(t *testing.T) {
	withApp(t, func() {
		app.CrawlDelay = 40 * time.Millisecond
		respectCrawlDelay("https://siril.org/a")
		start := time.Now()
		respectCrawlDelay("https://siril.org/b")
		if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
			t.Fatalf("crawl delay not applied: %v", elapsed)
		}
	})
}

func TestCapAuxiliarySets(t *testing.T) {
	withApp(t, func() {
		for i := 0; i < 100010; i++ {
			app.UniqueSignature[fmt.Sprintf("sig-%d", i)] = "url"
		}
		capAuxiliarySets()
		if len(app.UniqueSignature) != 0 {
			t.Fatalf("unique signature set was not capped: %d", len(app.UniqueSignature))
		}
	})
}

func TestSaveCrawlStateDirtyTracking(t *testing.T) {
	withApp(t, func() {
		dir := t.TempDir()
		store, err := storelib.Init(storelib.BackendLocal, filepath.Join(dir, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		app.CacheStore = store

		saveCrawlState()
		if store.Exists(crawlStateKey) {
			t.Fatal("clean state should not be written")
		}
		app.StateDirty = true
		saveCrawlState()
		if !store.Exists(crawlStateKey) {
			t.Fatal("dirty state was not written")
		}
		if app.StateDirty {
			t.Fatal("dirty flag was not cleared")
		}
	})
}

func TestLoadCrawlStateRejectsUnknownVersion(t *testing.T) {
	withApp(t, func() {
		app.CacheStore = &memoryStore{data: map[string]string{
			crawlStateKey: `{"version":999,"processed":42}`,
		}}
		if got := loadCrawlState(); got != 0 {
			t.Fatalf("unknown version accepted: %d", got)
		}
	})
}

func TestCompileConfigRegexes(t *testing.T) {
	withApp(t, func() {
		compileConfigRegexes()
		if reLinkOk == nil || reRankingKw == nil || reStopwords == nil || reBannedDomains == nil {
			t.Fatal("configuration regexes were not compiled")
		}
		if !reLinkOk.MatchString("https://siril.org/tutorials/stacking") {
			t.Fatal("link regex does not match a valid link")
		}
	})
}

func TestRejectionCountersAreCounted(t *testing.T) {
	withApp(t, func() {
		app.RejectionCounts = make(map[string]int)
		countRejection("duplicate")
		countRejection("duplicate")
		countRejection("too_short")
		summary := rejectionSummary()
		if len(summary) != 4 || summary[0] != "duplicate" || summary[1] != 2 || summary[2] != "too_short" {
			t.Fatalf("rejectionSummary = %v", summary)
		}
	})
}

func TestEndToEndLocalServer(t *testing.T) {
	body := strings.Repeat("Astronomical photographers combine exposures to increase signal and reveal faint nebulae galaxies planets while carefully tracking stars across dark skies night after night. ", 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(body))
	}))
	defer server.Close()

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crawler.yaml"), []byte(
		"regexBannedDomains: \"nomatch\"\nregexLinkBannedTokens: \"nomatch\"\n"+
			"engStopWordsWOthe: \"and|while|across|after|night\"\n"+
			"engLowRelevancyWords: \"\"\nspecialStopwords: \"\"\n"+
			"downloadTimeout: 2\nlogLevel: error\nrespectRobots: false\n"+
			"cacheBackend: local\ncachePath: ./cache/cache.db\nmaxDownloadMB: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seed := server.URL + "/stacking/images"
	profile := fmt.Sprintf("curatedDomains: |\n  127\\.0\\.0\\.1\nlinkOk: |\n  stack|image\n"+
		"regexRankingKeywords: |\n  (?i)\\W(stack|image|calibration|dark|flat|registration|integration)\\W\n"+
		"minDocLen: 10\nmaxDocLen: 100000\nscoreThreshold: 0\nrankingStrategy: hybrid\n"+
		"maxLinksPerPage: 10\nbootstrapingLinks:\n- %q\n", seed)
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", "local.yaml"), []byte(profile), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "corpusfreqlib"), 0755); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile("corpusfreqlib/freq-index.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corpusfreqlib", "freq-index.json.gz"), index, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	old := app
	defer func() { app = old }()
	app = newCrawler()
	app.ProfileName = "local"
	app.CLIWorkers = 2

	startIndex, cleanup, err := setupRuntime("local")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	final := crawlLoop(startIndex)
	reportResults(final)

	if app.CacheHits+app.OnlineDownloads == 0 {
		t.Fatal("no pages were processed")
	}
	if app.AcceptedDocs == 0 {
		t.Fatalf("no documents accepted; rejections=%v", app.RejectionCounts)
	}
	if got, err := os.ReadFile("logs/corpusCuratedText.log"); err != nil || !strings.Contains(string(got), "stacking") {
		t.Fatalf("curated corpus missing accepted text: err=%v", err)
	}
	if !iolibFileExists("output/corpusFrequencies.txt") {
		t.Fatal("frequency output was not written")
	}
}

func TestScoreWithRegexNoCapturingGroup(t *testing.T) {
	withApp(t, func() {
		re := regexp.MustCompile("stack|image|siril")
		got := scoreWithRegex("stack image siril stack", re)
		if got <= 0 {
			t.Fatalf("scoreWithRegex = %v, want > 0", got)
		}
		reGroup := regexp.MustCompile(`(?i)\W(stack|image)\W`)
		if scoreWithRegex(" stack image ", reGroup) <= 0 {
			t.Fatal("grouped regex should also score")
		}
	})
}

func TestResolveProfileNameCaseInsensitive(t *testing.T) {
	withApp(t, func() {
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("profiles", "skystacker-research.yaml")
		if _, err := os.Stat(path); err != nil {
			if err := os.WriteFile(path, []byte("scoreThreshold: 1\n"), 0644); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
		}
		got, err := resolveProfileName("Skystacker Research")
		if err != nil || got != "skystacker-research" {
			t.Fatalf("resolveProfileName = %q, %v", got, err)
		}
	})
}

func TestResetForProfile(t *testing.T) {
	withApp(t, func() {
		app.AcceptedDocs = 9
		app.CacheHits = 4
		app.LPool = []aLink{{URL: "https://a.org"}}
		app.RejectionCounts["non_english"] = 3
		app.GUILastError = "old"
		app.ResetForProfile("local")
		if app.AcceptedDocs != 0 || app.CacheHits != 0 || len(app.LPool) != 0 ||
			len(app.RejectionCounts) != 0 || app.GUILastError != "" || app.ProfileName != "local" {
			t.Fatalf("state not reset: %+v", app)
		}
	})
}
