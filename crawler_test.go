package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"goCrawler/storelib"
)

// withTempWorkDir runs fn in a temporary directory with a logs/ folder.
func withTempWorkDir(t *testing.T, fn func()) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
		t.Fatal(err)
	}
	for name := range app.RejectedLinkSets {
		app.RejectedLinkSets[name] = make(map[string]struct{})
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	}()
	fn()
}

// TestTokenize checks token splitting and counting.
func TestTokenize(t *testing.T) {
	got := tokenize("Hello, world! stack-ing 123")
	want := []string{"Hello", "world", "stack", "ing", "123"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
	if tokensCount("a b c") != 3 {
		t.Fatal("tokensCount should count three tokens")
	}
}

// TestSplitParagraphs checks paragraph splitting on line breaks.
func TestSplitParagraphs(t *testing.T) {
	got := splitParagraphs("first\n\nsecond\r\nthird")
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitParagraphs = %v, want %v", got, want)
	}
}

// TestGetDomainAndSecondLevelDomain checks host extraction.
func TestGetDomainAndSecondLevelDomain(t *testing.T) {
	if got := getDomain("https://www.siril.org/tutorials/"); got != "siril.org" {
		t.Fatalf("getDomain = %q, want siril.org", got)
	}
	if got := getSecondLevelDomain("https://www.siril.org/tutorials/"); got != "siril.org" {
		t.Fatalf("getSecondLevelDomain = %q, want siril.org", got)
	}
}

// TestRankingByKeywords checks keyword-based scoring.
func TestRankingByKeywords(t *testing.T) {
	setupTestConfig()
	text := "stack image noise signal siril stacking"
	if got := rankingByKeywords(text); got <= 0 {
		t.Fatalf("rankingByKeywords = %v, want > 0", got)
	}
	if got := rankingByKeywords("unrelated words without keywords"); got != 0 {
		t.Fatalf("rankingByKeywords without keywords = %v, want 0", got)
	}
}

// TestAnalyzeFiltersStopwords verifies that stopwords are removed.
func TestAnalyzeFiltersStopwords(t *testing.T) {
	setupTestConfig()
	tokens := analyze("The signal and the noise of the image")
	joined := strings.Join(tokens, " ")
	if !strings.Contains(joined, "signal") || !strings.Contains(joined, "noise") {
		t.Fatalf("analyze dropped meaningful tokens: %v", tokens)
	}
	if strings.Contains(joined, "the") {
		t.Fatalf("analyze kept stopwords: %v", tokens)
	}
}

// TestBestParagraph verifies selection of the highest-ranked paragraph.
func TestBestParagraph(t *testing.T) {
	setupTestConfig()
	best := bestParagraph([]string{
		"ordinary text with no relevant terms at all",
		"stack image noise signal siril stacking",
	})
	if !strings.Contains(best, "stack") {
		t.Fatalf("bestParagraph = %q, want the stacking paragraph", best)
	}
}

// TestFreqAndSort verifies frequency counting and sorting.
func TestFreqAndSort(t *testing.T) {
	setupTestConfig()
	f := make(freq)
	for _, token := range []string{"alpha", "beta", "beta", "gamma", "gamma", "gamma"} {
		f.add(token)
	}
	sorted := rSortFreq(f)
	if got := getKVkeys(sorted); !reflect.DeepEqual(got, []string{"gamma", "beta", "alpha"}) {
		t.Fatalf("rSortFreq keys = %v, want [gamma beta alpha]", got)
	}
}

// TestNgramsOf checks n-gram extraction.
func TestNgramsOf(t *testing.T) {
	setupTestConfig()
	got := ngramsOf("stack image noise", 2)
	if len(got) == 0 {
		t.Fatal("ngramsOf returned no bigrams")
	}
	if !strings.Contains(strings.Join(got, "|"), "stack") {
		t.Fatalf("ngramsOf = %v, expected stack bigrams", got)
	}
}

// TestLinkSeemsOk checks the URL validation chain.
func TestLinkSeemsOk(t *testing.T) {
	setupTestConfig()
	withTempWorkDir(t, func() {
		if !linkSeemsOk("https://siril.org/tutorials/stacking", false) {
			t.Fatal("whitelisted URL should pass")
		}
		if !linkSeemsOk("https://example.org/about", true) {
			t.Fatal("syntactically valid URL should pass when custom checks are skipped")
		}
		if linkSeemsOk("abc", false) {
			t.Fatal("too short URL should fail")
		}
		if linkSeemsOk("javascript:void(0)", false) {
			t.Fatal("javascript URL should fail")
		}
		if linkSeemsOk("https://facebook.com/profile", false) {
			t.Fatal("banned domain should fail")
		}
		if !iolibFileExists("logs/linksNotOk.log") {
			t.Fatal("rejected links should be logged")
		}
	})
}

// iolibFileExists is a small local helper to avoid touching the real working directory.
func iolibFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestDomainHadFailed checks failure counting without regex injection.
func TestDomainHadFailed(t *testing.T) {
	withTempWorkDir(t, func() {
		content := strings.Repeat("example.org\n", 7) + "a(b).com\n"
		if err := os.WriteFile("logs/domainFailed.log", []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		initFailedDomains()
		if !domainHadFailed("example.org") {
			t.Fatal("domain with 7 failures should be discarded")
		}
		if domainHadFailed("other.org") {
			t.Fatal("domain without failures should be allowed")
		}
		if domainHadFailed("a(b).com") {
			t.Fatal("regex metacharacters in a domain must not cause a false positive")
		}
	})
}

// TestLogRejectedLinkDeduplicates verifies log deduplication.
func TestLogRejectedLinkDeduplicates(t *testing.T) {
	withTempWorkDir(t, func() {
		logRejectedLink("linksNotOk.log", "https://example.org/bad")
		logRejectedLink("linksNotOk.log", "https://example.org/bad")
		b, err := os.ReadFile("logs/linksNotOk.log")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(b), "https://example.org/bad"); got != 1 {
			t.Fatalf("link was written %d times, want 1", got)
		}
	})
}

type memoryStore struct {
	data map[string]string
}

func (m *memoryStore) Exists(key string) bool      { _, ok := m.data[key]; return ok }
func (m *memoryStore) Get(key string) string       { return m.data[key] }
func (m *memoryStore) Set(key, value string) error { m.data[key] = value; return nil }
func (m *memoryStore) Close() error                { return nil }
func (m *memoryStore) Name() string                { return "memory" }

func TestIsBanned(t *testing.T) {
	setupTestConfig()
	if !isBanned("https://facebook.com/profile", "facebook.com") {
		t.Fatal("banned domain not detected")
	}
	if !isBanned("https://example.org/file.pdf", "example.org") {
		t.Fatal("banned token not detected")
	}
	if isBanned("https://siril.org/tutorials/stacking", "siril.org") {
		t.Fatal("valid link reported as banned")
	}
}

func TestStopwordCounts(t *testing.T) {
	setupTestConfig()
	text := "the image and the noise of the stack"
	if stopWordsCount(text) < 3 {
		t.Fatalf("stopWordsCount = %d, want >= 3", stopWordsCount(text))
	}
	if border := stopWordsOnBorderCount(text); border < 0 {
		t.Fatalf("stopWordsOnBorderCount = %d", border)
	}
	_ = lowRelevancyWordsOnBorderCount(text)
}

func TestLowercaseAndStemmerFilters(t *testing.T) {
	got := lowercaseFilter([]string{"IMAGE", "Noise", "42"})
	if got[0] != "image" || got[1] != "noise" {
		t.Fatalf("lowercaseFilter = %v", got)
	}
	stemmed := stemmerFilter([]string{"stacking", "images"})
	if len(stemmed) != 2 {
		t.Fatalf("stemmerFilter = %v", stemmed)
	}
}

func TestNgramsFreqsAndRemoveItem(t *testing.T) {
	setupTestConfig()
	text := "astrometry calibration astrometry calibration"
	ngrams := ngramsOf(text, 2)
	if len(ngrams) == 0 {
		t.Fatal("expected bigrams from ngramsOf")
	}
	freqs := ngramsFreqsOf(text, 2)
	for _, item := range freqs {
		if item.Value < 3 {
			t.Fatalf("ngramsFreqsOf kept low-frequency item: %+v", item)
		}
	}
	items := []kv{{Key: "a", Value: 1}, {Key: "b", Value: 2}}
	if got := kvSliceRemoveItem(items, 0); len(got) != 1 || got[0].Key != "b" {
		t.Fatalf("kvSliceRemoveItem = %v", got)
	}
	_ = ngramsFreqsOfAll(text, 3)
}

func TestLinkSeemsOkLengthBoundaries(t *testing.T) {
	setupTestConfig()
	withTempWorkDir(t, func() {
		if linkSeemsOk(strings.Repeat("a", 12), false) {
			t.Fatal("12-character link must fail")
		}
		if !linkSeemsOk("https://siril.org/"+strings.Repeat("a", 13), true) {
			t.Fatal("14+ character URL must pass syntax checks")
		}
	})
}

func TestIsBannedCountsAreStructured(t *testing.T) {
	app.RejectionCounts = make(map[string]int)
	countRejection("low_relevancy")
	countRejection("duplicate")
	countRejection("low_relevancy")
	summary := rejectionSummary()
	if len(summary) != 4 {
		t.Fatalf("rejectionSummary = %v, want 4 values", summary)
	}
	if summary[0] != "duplicate" || summary[2] != "low_relevancy" {
		t.Fatalf("rejectionSummary not sorted: %v", summary)
	}
}

func TestSaveLoadCrawlState(t *testing.T) {
	oldStore := app.CacheStore
	defer func() { app.CacheStore = oldStore }()
	dir := t.TempDir()
	store, err := storelib.Init(storelib.BackendLocal, dir+"/cache.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app.CacheStore = store

	app.LPool = []aLink{{URL: "https://siril.org/a", Domain: "siril.org", Count: 2, Status: 0}}
	app.DomainCounter = map[string]int{"siril.org": 3}
	app.UniqueSignature = map[string]string{"sig": "https://siril.org/a"}
	app.CorpusFreqs = freq{"stack": 7}
	app.LastProcessedLink = 42
	app.StateDirty = true
	saveCrawlState()

	app.LPool = nil
	app.DomainCounter = make(map[string]int)
	app.UniqueSignature = make(map[string]string)
	app.CorpusFreqs = make(freq)
	app.LastProcessedLink = 0

	if got := loadCrawlState(); got != 42 {
		t.Fatalf("loadCrawlState = %d, want 42", got)
	}
	if len(app.LPool) != 1 || app.DomainCounter["siril.org"] != 3 || app.CorpusFreqs["stack"] != 7 {
		t.Fatal("crawl state was not restored correctly")
	}
}

func TestStartSessionOutputNaming(t *testing.T) {
	withTempWorkDir(t, func() {
		stop := startSessionOutput("crypto")
		if stop == nil {
			t.Fatal("startSessionOutput returned nil")
		}
		stop()
		matches, err := filepath.Glob("logs/Crypto-*-Z??-??-??.txt")
		if err != nil || len(matches) != 1 {
			t.Fatalf("session file not created: %v %v", matches, err)
		}
	})
}

func TestDownloadCachedFromFakeStore(t *testing.T) {
	oldStore := app.CacheStore
	defer func() { app.CacheStore = oldStore }()
	payload, _ := json.Marshal(CachedData{Content: "cached text", Links: []string{"https://a.org"}})
	app.CacheStore = &memoryStore{data: map[string]string{"cache_https://a.org": string(payload)}}

	content, links, err := downloadCached("https://a.org")
	if err != nil || content != "cached text" || len(links) != 1 {
		t.Fatalf("downloadCached = %q %v %v", content, links, err)
	}
}

func TestProxyGetAndDownloadWithLocalServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><a href='https://example.org/stack'>stack</a><p>stack image noise signal siril stacking</p></body></html>"))
	}))
	defer srv.Close()

	app.DownloadTimeout = 3 * time.Second
	app.ProxyHost = ""
	setupTestConfig()

	withTempWorkDir(t, func() {
		initFileLoggers()
		defer closeFileLoggers()

		resp, err := proxyGet(srv.URL, false)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		content, links, err := download(srv.URL + "/page")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(content, "stack") || len(links) == 0 {
			t.Fatalf("download = %q, links = %v", content, links)
		}
	})
}

func TestFTS5IndexAndSearch(t *testing.T) {
	oldPath := app.CachePath
	app.CachePath = filepath.Join(t.TempDir(), "search.db")
	defer func() {
		app.CachePath = oldPath
		if app.SearchDB != nil {
			app.SearchDB.Close()
			app.SearchDB = nil
		}
	}()
	if err := initSearchIndex(); err != nil {
		t.Fatal(err)
	}
	indexDocument("https://example.org/stack", "stacking images and sigma clipping")
	var count int
	if err := app.SearchDB.QueryRow(`SELECT count(*) FROM corpus_fts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("corpus_fts has %d rows, want 1", count)
	}
	var got string
	if err := app.SearchDB.QueryRow(`SELECT url FROM corpus_fts WHERE corpus_fts MATCH ?`, "stacking").Scan(&got); err != nil {
		t.Fatalf("FTS5 MATCH failed: %v", err)
	}
	if got != "https://example.org/stack" {
		t.Fatalf("search result = %q", got)
	}
}

func TestParseCLISpaceSeparated(t *testing.T) {
	oldArgs, oldProfile, oldWorkers, oldDry, oldBackend, oldCache := os.Args, app.ProfileName, app.CLIWorkers, app.CLIDryRun, app.CacheBackend, app.CachePath
	defer func() {
		os.Args = oldArgs
		app.ProfileName, app.CLIWorkers, app.CLIDryRun, app.CacheBackend, app.CachePath = oldProfile, oldWorkers, oldDry, oldBackend, oldCache
	}()
	os.Args = []string{"crawler", "-profile", "crypto", "-workers", "4", "-dry-run", "-backend", "redis"}
	if err := parseCLI(); err != nil {
		t.Fatal(err)
	}
	if app.ProfileName != "crypto" || app.CLIWorkers != 4 || !app.CLIDryRun || app.CacheBackend != "redis" {
		t.Fatalf("parseCLI: profile=%s workers=%d dry=%v backend=%s", app.ProfileName, app.CLIWorkers, app.CLIDryRun, app.CacheBackend)
	}
}

func TestTopKFreq(t *testing.T) {
	f := freq{"a": 1, "b": 5, "c": 3, "d": 9, "e": 2}
	got := topKFreq(f, 2)
	if len(got) != 2 || got[0].Key != "d" || got[1].Key != "b" {
		t.Fatalf("topKFreq = %v", got)
	}
}

func TestGetSecondLevelDomainMultiLabelTLD(t *testing.T) {
	got := getSecondLevelDomain("https://www.example.co.uk/page")
	if got != "example.co.uk" {
		t.Fatalf("getSecondLevelDomain = %q, want example.co.uk", got)
	}
}

func TestPruneTerminalLinks(t *testing.T) {
	oldPool, oldIndex := app.LPool, app.LinkIndex
	defer func() { app.LPool, app.LinkIndex = oldPool, oldIndex }()
	app.LPool = []aLink{
		{URL: "https://a.org", Status: 2},
		{URL: "https://b.org", Status: 0},
		{URL: "https://c.org", Status: 3},
	}
	app.LinkIndex = map[string]int{"https://a.org": 0, "https://b.org": 1, "https://c.org": 2}
	pruneTerminalLinks()
	if len(app.LPool) != 1 || app.LPool[0].URL != "https://b.org" {
		t.Fatalf("pruned pool = %v", app.LPool)
	}
	if app.LinkIndex["https://b.org"] != 0 {
		t.Fatalf("index not rebuilt: %v", app.LinkIndex)
	}
}

func TestParseCLIVersionAndLicense(t *testing.T) {
	oldArgs, oldV, oldL := os.Args, app.ShowVersion, app.ShowLicense
	defer func() { os.Args, app.ShowVersion, app.ShowLicense = oldArgs, oldV, oldL }()
	os.Args = []string{"crawler", "-version", "-license"}
	app.ShowVersion, app.ShowLicense = false, false
	if err := parseCLI(); err != errStop {
		t.Fatalf("parseCLI = %v, want errStop", err)
	}
	if !app.ShowVersion || !app.ShowLicense {
		t.Fatal("both informational flags should be set")
	}
}

func TestSemanticRanking(t *testing.T) {
	old := app.RankingStrategy
	defer func() { app.RankingStrategy = old }()
	setupTestConfig()
	app.RankingStrategy = "semantic"
	relevant := semanticScore("stack stacking integration calibration astrometry image noise")
	unrelated := semanticScore("grocery shopping vegetables recipe cooking")
	if relevant <= unrelated {
		t.Fatalf("semanticScore relevant=%v unrelated=%v", relevant, unrelated)
	}
	if v := tokenEmbedding("stacking"); len(v) != embeddingDim {
		t.Fatalf("tokenEmbedding length = %d", len(v))
	}
}

func TestConfigKeysLoad(t *testing.T) {
	old := app
	defer func() { app = old }()
	app = newCrawler()
	app.ProfileName = "astrophotography"
	if err := yamlInitGeneral(); err != nil {
		t.Fatal(err)
	}
	if err := yamlInitSpecific(); err != nil {
		t.Fatal(err)
	}
	if app.MinDocLen != 50 || app.MaxDocLen != 100000 || app.CacheBackend != "local" {
		t.Fatalf("config keys not loaded: min=%d max=%d backend=%s", app.MinDocLen, app.MaxDocLen, app.CacheBackend)
	}
}

func TestGUIPortDefault(t *testing.T) {
	c := newCrawler()
	if c.GUIPort != 8765 {
		t.Fatalf("default GUI port = %d, want 8765", c.GUIPort)
	}
}
