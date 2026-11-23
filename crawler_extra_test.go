package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"

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

	if app.CacheHits.Load()+app.OnlineDownloads.Load() == 0 {
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
	states := make(map[string]bool)
	for _, event := range app.QueueEvents {
		states[event.State] = true
	}
	if !states["requested"] || !states["retrieved"] {
		t.Fatalf("queue events missing requested/retrieved: %v", states)
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
		app.CacheHits.Store(4)
		app.LPool = []aLink{{URL: "https://a.org"}}
		app.RejectionCounts["non_english"] = 3
		app.GUILastError = "old"
		app.ResetForProfile("local")
		if app.AcceptedDocs != 0 || app.CacheHits.Load() != 0 || len(app.LPool) != 0 ||
			len(app.RejectionCounts) != 0 || app.GUILastError != "" || app.ProfileName != "local" {
			t.Fatalf("state not reset: %+v", app)
		}
	})
}

func TestNonEnglishRejectionRecordsDetails(t *testing.T) {
	withApp(t, func() {
		app.TargetLanguage = "English"
		text := "Ceci est un texte ecrit en francais avec beaucoup de mots pour que la detection puisse fonctionner correctement et identifier la langue utilisee ici."
		paragraphs := filterParagraphs([]string{text}, "https://example.org/fr")
		if paragraphs[0] != "" {
			t.Fatal("non-English paragraph should be dropped")
		}
		events := app.RejectionEvents["non_english"]
		if len(events) != 1 {
			t.Fatalf("expected 1 rejection event, got %d", len(events))
		}
		if !strings.Contains(events[0].Detail, "detected") || events[0].Text == "" || events[0].URL == "" {
			t.Fatalf("incomplete rejection event: %+v", events[0])
		}
	})
}

func TestVisitedURLsPreventReaddingAfterPrune(t *testing.T) {
	withApp(t, func() {
		app.RegexLinkOk = "(?i)^https*://.*(stack)"
		app.CuratedDomains = "example\\.org"
		compileConfigRegexes()

		if !addLink("https://example.org/stack", false) {
			t.Fatal("first add failed")
		}
		if len(app.LPool) != 1 {
			t.Fatalf("pool = %d", len(app.LPool))
		}
		_, _ = getNextLink()
		app.LPool[0].Status = 2
		pruneTerminalLinks()
		if len(app.LPool) != 0 {
			t.Fatalf("pool after prune = %d", len(app.LPool))
		}
		if addLink("https://example.org/stack", false) {
			t.Fatal("visited URL was re-added after pruning")
		}
		if len(app.LPool) != 0 {
			t.Fatalf("pool after re-add = %d", len(app.LPool))
		}
	})
}

func TestConcurrentAddLinkIsSafe(t *testing.T) {
	withApp(t, func() {
		app.RegexLinkOk = "(?i)^https*://.*(stack)"
		app.CuratedDomains = "example\\.org"
		compileConfigRegexes()
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					addLink("https://example.org/stack", false)
				}
			}()
		}
		wg.Wait()
		if len(app.LPool) != 1 {
			t.Fatalf("pool = %d, want 1 unique URL", len(app.LPool))
		}
	})
}

func TestStaleReservedLinkIsRecovered(t *testing.T) {
	withApp(t, func() {
		app.RegexLinkOk = "(?i)^https*://.*(stack)"
		app.CuratedDomains = "example\\.org"
		compileConfigRegexes()
		addLink("https://example.org/stack", false)
		app.LPool[0].Status = 1
		app.LPool[0].ReservedAt = time.Now().Add(-2 * linkStaleTimeout)
		_, url := getNextLink()
		if url != "https://example.org/stack" {
			t.Fatalf("stale link was not recovered: %q", url)
		}
	})
}

func testPDFBytes(text string) []byte {
	var buf bytes.Buffer
	offsets := make([]int, 6)
	buf.WriteString("%PDF-1.4\n")
	offsets[1] = buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	offsets[3] = buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")
	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET\n"
	offsets[4] = buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n", len(stream))
	buf.WriteString(stream)
	buf.WriteString("endstream\nendobj\n")
	offsets[5] = buf.Len()
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	xref := buf.Len()
	buf.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return buf.Bytes()
}

func TestPDFURLGate(t *testing.T) {
	withApp(t, func() {
		link := "https://siril.org/files/doc.pdf"
		if !isBanned(link, "siril.org") {
			t.Fatal("PDF URL accepted while process_pdf is false")
		}
		app.ProcessPDF = true
		if isBanned(link, "siril.org") {
			t.Fatal("PDF URL rejected while process_pdf is true")
		}
		if !isBanned(link, "facebook.com") {
			t.Fatal("domain ban should still apply")
		}
		if !isBanned("https://siril.org/login", "siril.org") {
			t.Fatal("non-PDF banned token should still apply")
		}
	})
}

func TestDownloadCachedPDF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(testPDFBytes("Hello PDF world"))
	}))
	defer server.Close()

	withApp(t, func() {
		withTempWorkDir(t, func() {
			if err := initFileLoggers(); err != nil {
				t.Fatal(err)
			}
			defer closeFileLoggers()
			app.CacheStore = &memoryStore{data: map[string]string{}}
			app.HTTPClient = &http.Client{Timeout: 2 * time.Second}
			app.DownloadTimeout = 2 * time.Second

			if _, _, err := downloadCached(server.URL + "/doc.pdf"); err == nil {
				t.Fatal("PDF accepted while process_pdf is false")
			}

			app.ProcessPDF = true
			content, links, err := downloadCached(server.URL + "/doc.pdf")
			if err != nil || !strings.Contains(content, "Hello PDF world") || len(links) != 0 {
				t.Fatalf("PDF download: content=%q links=%v err=%v", content, links, err)
			}
			cached, _, err := downloadCached(server.URL + "/doc.pdf")
			if err != nil || cached != content {
				t.Fatalf("PDF cache: content=%q err=%v", cached, err)
			}
		})
	})
}

func TestNotificationWebhookAndThresholdOnce(t *testing.T) {
	payloads := make(chan map[string]any, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		payloads <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	withApp(t, func() {
		app.NotifyWebhookURL = server.URL
		app.NotifyDesktop = false
		app.RejectionNotifyThreshold = 2

		notify("manual", "hello", nil)
		select {
		case payload := <-payloads:
			if payload["event"] != "manual" || payload["message"] != "hello" {
				t.Fatalf("unexpected payload: %v", payload)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("webhook notification was not delivered")
		}

		for i := 0; i < 3; i++ {
			recordRejection("custom_reason", "", "", "")
		}
		select {
		case payload := <-payloads:
			if payload["event"] != "rejection_threshold" || payload["reason"] != "custom_reason" {
				t.Fatalf("unexpected threshold payload: %v", payload)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("threshold notification was not delivered")
		}
		select {
		case payload := <-payloads:
			t.Fatalf("threshold notification sent more than once: %v", payload)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func TestNotifyCompletionCanBeDisabled(t *testing.T) {
	payloads := make(chan map[string]any, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		payloads <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	withApp(t, func() {
		app.NotifyWebhookURL = server.URL
		app.NotifyOnCompletion = false
		notifyCompletion(5)
		select {
		case payload := <-payloads:
			t.Fatalf("completion notification sent while disabled: %v", payload)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func TestEnsureModuleRoot(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	root := t.TempDir()
	for _, name := range []string{"go.mod", "crawler.yaml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("module goCrawler\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "profiles")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	ensureModuleRoot()
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("working directory = %q, want module root %q", got, root)
	}
}

func TestTechnicalStopwordRatio(t *testing.T) {
	text := "Photometric calibration residuals decrease when regression matrices are factorized iteratively. " +
		"Detector response characterization requires repeated variance estimation across exposure sequences. " +
		"Signal extraction uses robust median statistics."

	withApp(t, func() {
		setupTestConfig()
		app.MinStopwordRatio = 0.1
		app.MaxStopwordRatio = 0.38
		if got := filterParagraphs([]string{text}, "https://example.org/article")[0]; got != "" {
			t.Fatal("technical paragraph should be rejected with the classic 0.1 minimum")
		}

		app.MinStopwordRatio = 0
		paragraphs := filterParagraphs([]string{text}, "https://example.org/article")
		if paragraphs[0] == "" {
			t.Fatal("technical paragraph should survive with minStopwordRatio = 0")
		}
		if content := buildCuratedContent(paragraphs); !strings.Contains(content, "Photometric calibration") {
			t.Fatalf("technical content missing from curated output: %q", content)
		}
	})
}

func TestProfileStopwordRatioKeys(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	dir := t.TempDir()
	profile := `curatedDomains: "example\\.org"
linkOk: "article"
regexRankingKeywords: "(?i)\\W(data|method)\\W"
bootstrapingLinks: ["https://example.org/article"]
minDocLen: 200
maxDocLen: 100000
scoreThreshold: 10
minStopwordRatio: 0.05
maxStopwordRatio: 0.55
`
	if err := os.WriteFile(filepath.Join(dir, "tech.yaml"), []byte(profile), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	withApp(t, func() {
		app.ProfileName = "tech"
		if err := yamlInitSpecific(); err != nil {
			t.Fatalf("yamlInitSpecific: %v", err)
		}
		if app.MinStopwordRatio != 0.05 || app.MaxStopwordRatio != 0.55 {
			t.Fatalf("ratios = %.2f/%.2f, want 0.05/0.55", app.MinStopwordRatio, app.MaxStopwordRatio)
		}
		if err := validateConfig(); err != nil {
			t.Fatalf("valid ratio range rejected: %v", err)
		}
		app.MinStopwordRatio = 0.8
		if err := validateConfig(); err == nil {
			t.Fatal("inverted ratio range accepted")
		}
	})
}

func TestDefaultBannedDomainsAllowsWikipedia(t *testing.T) {
	withApp(t, func() {
		vp := viper.New()
		vp.SetConfigFile("crawler.yaml")
		if err := vp.ReadInConfig(); err != nil {
			t.Fatalf("read crawler.yaml: %v", err)
		}
		app.RegexBannedDomains = vp.GetString("regexBannedDomains")
		compileConfigRegexes()

		if isBanned("https://en.wikipedia.org/wiki/Andromeda_Galaxy", "en.wikipedia.org") {
			t.Fatal("default config must not ban en.wikipedia.org")
		}
		if !isBanned("https://facebook.com/profile", "facebook.com") {
			t.Fatal("default config must still ban facebook.com")
		}
	})
}

func TestEnsureModuleRootFromForeignDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(original)
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ensureModuleRoot()
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Fatalf("working directory = %q, want module root %q", got, original)
	}
}

func TestStopwordRatioCLIFlags(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"crawler", "-profile=tech", "-min-stopword-ratio=0.02", "--max-stopword-ratio=0.9"}
	withApp(t, func() {
		if err := parseCLI(); err != nil {
			t.Fatalf("parseCLI: %v", err)
		}
		if app.MinStopwordRatio != 0.02 || app.MaxStopwordRatio != 0.9 {
			t.Fatalf("ratios = %.2f/%.2f, want 0.02/0.90", app.MinStopwordRatio, app.MaxStopwordRatio)
		}
	})
}

func TestGeneralConfigStopwordRatios(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	dir := t.TempDir()
	config := `regexBannedDomains: "nomatch"
regexLinkBannedTokens: "nomatch"
engStopWordsWOthe: "a|and|of|the|with"
engLowRelevancyWords: "|news|info"
specialStopwords: ""
minStopwordRatio: 0.05
maxStopwordRatio: 0.75
`
	if err := os.WriteFile(filepath.Join(dir, "crawler.yaml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	viper.Reset()
	defer viper.Reset()
	withApp(t, func() {
		if err := yamlInitGeneral(); err != nil {
			t.Fatalf("yamlInitGeneral: %v", err)
		}
		if app.MinStopwordRatio != 0.05 || app.MaxStopwordRatio != 0.75 {
			t.Fatalf("ratios = %.2f/%.2f, want 0.05/0.75", app.MinStopwordRatio, app.MaxStopwordRatio)
		}
		// CLI values win over both general and profile configuration.
		app.minRatioFromCLI = true
		app.MinStopwordRatio = 0.3
		if err := yamlInitGeneral(); err != nil {
			t.Fatalf("yamlInitGeneral with CLI override: %v", err)
		}
		if app.MinStopwordRatio != 0.3 {
			t.Fatalf("CLI ratio overwritten: %.2f", app.MinStopwordRatio)
		}
	})
}

func TestLintProfile(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0755); err != nil {
		t.Fatal(err)
	}
	valid := `curatedDomains: "example\\.org"
linkOk: "article"
regexRankingKeywords: "(?i)\\W(data|method)\\W"
bootstrapingLinks: ["https://example.org/article"]
minDocLen: 50
maxDocLen: 100000
minStopwordRatio: 0.0
maxStopwordRatio: 0.6
`
	invalid := valid + `bogusKey: 1
anotherBadSeed: ["not-a-url"]
`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "valid.yaml"), []byte(valid), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", "invalid.yaml"), []byte(invalid), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	withApp(t, func() {
		if err := lintProfile("valid"); err != nil {
			t.Fatalf("valid profile rejected: %v", err)
		}
		if err := lintProfile("invalid"); err == nil {
			t.Fatal("invalid profile accepted")
		}
	})
}

func TestTechnicalProfilePack(t *testing.T) {
	matches, err := filepath.Glob("profiles/examples/technical-*.yaml")
	if err != nil || len(matches) < 3 {
		t.Fatalf("expected at least 3 technical example profiles, got %d (%v)", len(matches), err)
	}
	withApp(t, func() {
		for _, path := range matches {
			name := "examples/" + strings.TrimSuffix(filepath.Base(path), ".yaml")
			if err := lintProfile(name); err != nil {
				t.Errorf("lint %s: %v", name, err)
			}
		}
	})
}

func TestRejectTraceAndReport(t *testing.T) {
	withTempWorkDir(t, func() {
		withApp(t, func() {
			app.RejectTracePath = "./logs/rejections.jsonl"
			app.ProfileName = "technical"
			app.BootstrapLinks = []string{"https://example.org/start"}
			if err := openRejectTrace(); err != nil {
				t.Fatalf("openRejectTrace: %v", err)
			}
			recordRejection("stopword_ratio", "https://example.org/start", "below 0.1", "Photometric calibration residuals")
			recordRejection("too_short", "https://example.org/other", "length 10", "")
			closeRejectTrace()

			events, err := readRejectTrace(app.RejectTracePath)
			if err != nil || len(events) != 2 {
				t.Fatalf("readRejectTrace = %d events, %v", len(events), err)
			}
			if events[0].Reason != "stopword_ratio" || events[0].Profile != "technical" {
				t.Fatalf("unexpected event metadata: %+v", events[0])
			}
			if events[0].Seed != "https://example.org/start" || events[1].Seed != "" {
				t.Fatalf("unexpected seed attribution: %+v", events)
			}
			reportPath := "./logs/rejection-report.txt"
			if err := writeRejectionReport(events, reportPath); err != nil {
				t.Fatalf("writeRejectionReport: %v", err)
			}
			body, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "stopword_ratio: 1") || !strings.Contains(string(body), "Photometric calibration") {
				t.Fatalf("report missing breakdown or sample:\n%s", body)
			}
		})
	})
}

func TestRejectReportAndTraceModes(t *testing.T) {
	withTempWorkDir(t, func() {
		withApp(t, func() {
			app.RejectTracePath = "./logs/rejections.jsonl"
			if err := openRejectTrace(); err != nil {
				t.Fatal(err)
			}
			recordRejection("non_english", "https://example.org/french", "detected fra", "Bonjour le monde")
			closeRejectTrace()

			if err := rejectReportFile(app.RejectTracePath); err != nil {
				t.Fatalf("rejectReportFile: %v", err)
			}
			if err := traceRejectedURL(app.RejectTracePath, "french"); err != nil {
				t.Fatalf("traceRejectedURL: %v", err)
			}
			if err := traceRejectedURL(app.RejectTracePath, "absent"); err == nil {
				t.Fatal("missing URL filter should fail")
			}
		})
	})
}

func TestCrawlStateChecksumRoundTrip(t *testing.T) {
	withApp(t, func() {
		store := &memoryStore{data: map[string]string{}}
		app.CacheStore = store
		app.StateDirty = true
		app.LastProcessedLink = 42
		saveCrawlState()

		if got := loadCrawlState(); got != 42 {
			t.Fatalf("restored index = %d, want 42", got)
		}

		tampered := strings.Replace(store.data[crawlStateKey], `"processed":42`, `"processed":99`, 1)
		if tampered == store.data[crawlStateKey] {
			t.Fatal("test fixture did not change the stored state")
		}
		store.data[crawlStateKey] = tampered
		if got := loadCrawlState(); got != 0 {
			t.Fatalf("tampered state accepted: %d", got)
		}
	})
}

func TestLoadLegacyCrawlStateWithoutChecksum(t *testing.T) {
	withApp(t, func() {
		app.CacheStore = &memoryStore{data: map[string]string{
			crawlStateKey: `{"version":1,"processed":42}`,
		}}
		if got := loadCrawlState(); got != 42 {
			t.Fatalf("legacy state not accepted: %d", got)
		}
	})
}

func TestRotateSessionLogsByAge(t *testing.T) {
	withTempWorkDir(t, func() {
		withApp(t, func() {
			oldFile := "./logs/Crypto-2026-01-02-Z10-00-00.txt"
			newFile := "./logs/Crypto-2026-11-23-Z10-00-00.txt"
			if err := os.WriteFile(oldFile, []byte("old session"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(newFile, []byte("new session"), 0644); err != nil {
				t.Fatal(err)
			}
			oldTime := time.Now().Add(-40 * 24 * time.Hour)
			if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
				t.Fatal(err)
			}
			app.LogRotateDays = 14
			app.LogRotateMaxMB = 500
			if err := rotateSessionLogs(); err != nil {
				t.Fatalf("rotateSessionLogs: %v", err)
			}
			if _, err := os.Stat(oldFile + ".gz"); err != nil {
				t.Fatal("old session was not gzipped")
			}
			if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
				t.Fatal("old plaintext session still present")
			}
			if _, err := os.Stat(newFile); err != nil {
				t.Fatal("recent session was rotated")
			}
		})
	})
}

func TestRotateSessionLogsBySize(t *testing.T) {
	withTempWorkDir(t, func() {
		withApp(t, func() {
			older := "./logs/A-2026-11-23-Z10-00-00.txt"
			newer := "./logs/B-2026-11-23-Z11-00-00.txt"
			if err := os.WriteFile(older, bytes.Repeat([]byte("x"), 600000), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(newer, bytes.Repeat([]byte("y"), 600000), 0644); err != nil {
				t.Fatal(err)
			}
			oldTime := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(older, oldTime, oldTime); err != nil {
				t.Fatal(err)
			}
			app.LogRotateDays = 0
			app.LogRotateMaxMB = 1
			if err := rotateSessionLogs(); err != nil {
				t.Fatalf("rotateSessionLogs: %v", err)
			}
			if _, err := os.Stat(older + ".gz"); err != nil {
				t.Fatal("oldest session was not gzipped by size cap")
			}
			if _, err := os.Stat(newer); err != nil {
				t.Fatal("newest session should remain uncompressed")
			}
		})
	})
}
