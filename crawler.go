// Package main implements a topic-restricted web crawler that builds curated English corpora.
package main

import (
	"bytes"
	"container/heap"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/jackdanger/collectlinks"
	snowballeng "github.com/kljensen/snowball/english"
	"github.com/spf13/viper"
	"jaytaylor.com/html2text"
	"log/slog"

	"github.com/abadojack/whatlanggo"

	"github.com/computerphysicslab/goPackages/goDebug"

	"goCrawler/corpusfreqlib"
	"goCrawler/iolib"
	"goCrawler/robotspolicy"
	"goCrawler/storelib"
	"goCrawler/stringlib"
)

// Crawler owns the configuration, runtime state and stores of one crawl.
// Keeping them together removes package-level mutable globals and makes the
// crawler embeddable and independently testable.
type Crawler struct {
	// configuration
	RegexBannedDomains    string
	RegexLinkBannedTokens string
	CuratedDomains        string
	RegexLinkOk           string
	EngStopWordsWOthe     string
	EngStopWords          string
	EngLowRelevancyWords  string
	RegexStopwords        string
	RegexRankingKeywords  string
	ReRankingExtended     *regexp.Regexp
	ProxyHost             string
	ProxyUser             string
	ProxyPass             string
	DownloadTimeout       time.Duration
	MaxDownloadBytes      int64
	BootstrapLinks        []string
	MinDocLen             int
	MaxDocLen             int
	MaxLinksPerPage       int
	CrawlDelay            time.Duration
	RankingStrategy       string
	Synonyms              map[string][]string
	ScoreThreshold        float64
	AddURL                string
	CacheBackend          string
	CachePath             string
	UserAgent             string
	RespectRobots         bool
	LogLevel              string
	ProfileName           string
	TargetLanguage        string
	MinLanguageConfidence float64
	ConfigFile            string
	CLIWorkers            int
	CLIDryRun             bool
	CLIQuery              string
	CLIExplore            bool
	CLIGenerate           string
	ShowVersion           bool
	ShowLicense           bool
	GUIPort               int
	GUILastError          string
	ResumeEnabled         bool

	// state
	LPool             []aLink
	LinkIndex         map[string]int
	DomainCounter     map[string]int
	FailedDomains     map[string]int
	UniqueSignature   map[string]string
	CorpusFreqs       freq
	CorpusDocFreq     map[string]int
	CorpusDocCount    int
	RejectionCounts   map[string]int
	AcceptedDocs      int
	LastProcessedLink int
	StateDirty        bool
	CacheHits         int
	OnlineDownloads   int
	DownloadMS        int64
	RejectedLinkSets  map[string]map[string]struct{}
	DomainLastReq     map[string]time.Time
	Counter           int

	// stores and writers
	CacheStore      storelib.Store
	RobotsPolicy    *robotspolicy.Policy
	HTTPClient      *http.Client
	SearchDB        *sql.DB
	CSVWriter       *csv.Writer
	CSVFile         *os.File
	Logger          *slog.Logger
	LogDownload     *log.Logger
	LogCGI          *log.Logger
	LogDomainFailed *log.Logger
	LogFiles        []*os.File

	// synchronization
	PoolMu        sync.Mutex
	RejectMu      sync.Mutex
	FailMu        sync.Mutex
	CrawlMu       sync.Mutex
	DomainDelayMu sync.Mutex
	StopRequested atomic.Bool
}

// newCrawler returns a crawler with sane defaults.
func newCrawler() *Crawler {
	return &Crawler{
		MaxDownloadBytes: 5 << 20,
		RankingStrategy:  "hybrid",
		Synonyms:         make(map[string][]string),
		LinkIndex:        make(map[string]int),
		DomainCounter:    make(map[string]int),
		FailedDomains:    make(map[string]int),
		UniqueSignature:  make(map[string]string),
		CorpusFreqs:      make(freq),
		CorpusDocFreq:    make(map[string]int),
		RejectionCounts:  make(map[string]int),
		RejectedLinkSets: map[string]map[string]struct{}{
			"linksNotOk.log":                 {},
			"linksNotOk.additional.log":      {},
			"linksNotOk.ParseRequestURI.log": {},
			"robotsBlocked.log":              {},
		},
		DomainLastReq:   make(map[string]time.Time),
		HTTPClient:      &http.Client{Timeout: 10 * time.Second},
		Logger:          slog.New(slog.NewTextHandler(os.Stdout, nil)),
		LogDownload:     log.New(io.Discard, "", 0),
		LogCGI:          log.New(io.Discard, "", 0),
		LogDomainFailed: log.New(io.Discard, "", 0),
		CLIWorkers:      1,
		GUIPort:         8765,
		TargetLanguage:  "English",
		CachePath:       "./cache/cache.db",
		LogLevel:        "info",
	}
}

// app is the active crawler instance, initialized at the start of run().
var app = newCrawler()

/******************************************************************************/
/******************************************************************************/
/*********************** CONFIG ***********************************************/
/******************************************************************************/
/******************************************************************************/

// initHTTPClient builds the shared HTTP client with connection reuse.
func initHTTPClient() {
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   app.DownloadTimeout,
		ResponseHeaderTimeout: app.DownloadTimeout,
	}
	if app.ProxyHost != "" {
		transport.Proxy = http.ProxyURL(&url.URL{
			Scheme: "http",
			User:   url.UserPassword(app.ProxyUser, app.ProxyPass),
			Host:   app.ProxyHost,
		})
	}
	app.HTTPClient = &http.Client{Transport: transport, Timeout: app.DownloadTimeout}
}

var (
	reWww          = regexp.MustCompile(`^www\.(.*)$`)
	reSecondLevel  = regexp.MustCompile(`^.*?([^\.]+\.[^\.]+)$`)
	reURLStrict    = regexp.MustCompile(`(?i)(^http(s)?://[a-z0-9-]+(\.[a-z0-9-]+)*(:[0-9]+)?(\/[a-z0-9\-\_\/\.\+\%\(\)\~\@]*)?$)`)
	reCgiParams    = regexp.MustCompile(`\?.*$`)
	reParagraph    = regexp.MustCompile(`[\r\n]+`)
	reNonWord      = regexp.MustCompile(`(\W+)`)
	reAvoidChars   = regexp.MustCompile(`([*\(\)\?\-\,\:\#\[\]\"]+)`)
	reDoubleSpace  = regexp.MustCompile(`(\W)\W`)
	reWordMatch    = regexp.MustCompile(`(?i)\W([^\W]+)\W`)
	reNGramsIgnore = regexp.MustCompile(`(?i)\W(cite_note|cite_ref|https*)\W`)
	reRemoveUrls   = regexp.MustCompile(`(?i)\W([^ \t]*/[^ \t]*)\W`)
	reRemoveHtml   = regexp.MustCompile(`(?i)(<(p|img|div)[^>]*>)`)
	reLongWords    = regexp.MustCompile(`(?i)\W([^ \t\n]{80,})\W`)
	reMarkup       = regexp.MustCompile(`(?i)\W(div|img|nofollow|javascript:|(alt|class|style|width|height|onclick)="[^"]*")\W`)
)

var (
	reBannedDomains *regexp.Regexp
	reBannedTokens  *regexp.Regexp
	reLinkOk        *regexp.Regexp
	reRankingKw     *regexp.Regexp
	reStopwords     *regexp.Regexp
	reStopWords     *regexp.Regexp
	reStopLeft      *regexp.Regexp
	reStopRight     *regexp.Regexp
	reLowRelLeft    *regexp.Regexp
)

// compileConfigRegexes compiles the regexes derived from configuration once.
func compileConfigRegexes() {
	reBannedDomains = regexp.MustCompile(app.RegexBannedDomains)
	reBannedTokens = regexp.MustCompile(app.RegexLinkBannedTokens)
	reStopwords = regexp.MustCompile(app.RegexStopwords)
	reStopWords = regexp.MustCompile(`(?i)\W(` + app.EngStopWords + `)\W`)
	reStopLeft = regexp.MustCompile(`(?i)^(` + app.EngStopWords + `)\W`)
	reStopRight = regexp.MustCompile(`(?i)\W(` + app.EngStopWords + `)$`)
	reLowRelLeft = regexp.MustCompile(`(?i)^(` + app.EngStopWords + app.EngLowRelevancyWords + `)\W`)
	reLinkOk = regexp.MustCompile(app.RegexLinkOk)
	reRankingKw = regexp.MustCompile(app.RegexRankingKeywords)
}

// app.Logger is replaced at startup so records are written through the session tee.

const maxUniqueRejectedLinks = 1000000

const crawlStateKey = "state:crawl"

// crawlState is the serializable snapshot needed to resume a run.
const crawlStateVersion = 1

type crawlState struct {
	Version       int               `json:"version"`
	Pool          []aLink           `json:"pool"`
	DomainCounter map[string]int    `json:"domainCounter"`
	FailedDomains map[string]int    `json:"failedDomains"`
	Processed     int               `json:"processed"`
	UniqueSigs    map[string]string `json:"uniqueSignatures"`
	Frequencies   map[string]int    `json:"frequencies"`
	DocFreq       map[string]int    `json:"docFreq"`
	DocCount      int               `json:"docCount"`
	SavedAt       time.Time         `json:"savedAt"`
}

// saveCrawlState stores the current crawl progress in the cache store.
func saveCrawlState() {
	if !app.StateDirty {
		return
	}
	app.PoolMu.Lock()
	app.FailMu.Lock()
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	defer app.FailMu.Unlock()
	defer app.PoolMu.Unlock()
	state := crawlState{
		Version:       crawlStateVersion,
		Pool:          app.LPool,
		DomainCounter: app.DomainCounter,
		FailedDomains: app.FailedDomains,
		Processed:     app.LastProcessedLink,
		UniqueSigs:    app.UniqueSignature,
		Frequencies:   app.CorpusFreqs,
		DocFreq:       app.CorpusDocFreq,
		DocCount:      app.CorpusDocCount,
		SavedAt:       time.Now(),
	}
	b, err := json.Marshal(state)
	if err != nil {
		app.Logger.Error("cannot serialize crawl state", "error", err)
		return
	}
	if err := app.CacheStore.Set(crawlStateKey, string(b)); err != nil {
		app.Logger.Error("cannot save crawl state", "error", err)
		return
	}
	app.StateDirty = false
	app.Logger.Debug("crawl state saved", "processed", app.LastProcessedLink)
}

// loadCrawlState restores a previous crawl and returns the next link index.
func loadCrawlState() int {
	value := app.CacheStore.Get(crawlStateKey)
	if value == "" {
		app.Logger.Warn("no crawl state found, starting from scratch")
		return 0
	}
	var state crawlState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		app.Logger.Error("cannot decode crawl state", "error", err)
		return 0
	}
	if state.Version != crawlStateVersion {
		app.Logger.Warn("unsupported crawl state version", "version", state.Version, "expected", crawlStateVersion)
		return 0
	}
	app.LPool = state.Pool
	app.LinkIndex = make(map[string]int, len(app.LPool))
	for i, link := range app.LPool {
		app.LinkIndex[link.URL] = i
	}
	app.DomainCounter = state.DomainCounter
	if state.FailedDomains != nil {
		app.FailedDomains = state.FailedDomains
	}
	app.UniqueSignature = state.UniqueSigs
	app.CorpusFreqs = state.Frequencies
	if state.DocFreq != nil {
		app.CorpusDocFreq = state.DocFreq
	}
	app.CorpusDocCount = state.DocCount
	app.LastProcessedLink = state.Processed
	app.Logger.Info("crawl state restored",
		"processed", state.Processed,
		"links", len(app.LPool),
		"savedAt", state.SavedAt)
	return state.Processed
}

// initLogger configures the structured app.Logger level.
func initLogger(level string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	app.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// parseSynonyms reads "term: a|b" lines into a lookup map.
func parseSynonyms(raw string) map[string][]string {
	result := make(map[string][]string)
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		for _, value := range strings.Split(parts[1], "|") {
			value = strings.TrimSpace(value)
			if value != "" {
				result[strings.ToLower(parts[0])] = append(result[strings.ToLower(parts[0])], value)
			}
		}
	}
	return result
}

// buildRankingRegex merges profile keywords and app.Synonyms into one matcher.
func buildRankingRegex() {
	terms := regexTermList(app.RegexRankingKeywords)
	for _, alts := range app.Synonyms {
		terms = append(terms, alts...)
	}
	terms = listUnique(terms)
	app.ReRankingExtended = regexp.MustCompile(`(?i)\W(` + strings.Join(terms, "|") + `)\W`)
}

// regexTermList extracts the alternatives of a (?-style)\W(...)\W regex.
func regexTermList(pattern string) []string {
	value := pattern
	for _, prefix := range []string{`(?i)\W(`, `\W(`} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimPrefix(value, prefix)
			break
		}
	}
	value = strings.TrimSuffix(value, `)\W`)
	return listUnique(strings.Split(value, "|"))
}

// listUnique removes empty and duplicate strings while preserving order.
func listUnique(in []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// respectCrawlDelay applies a generic per-domain delay between requests.
func respectCrawlDelay(rawURL string) {
	if app.CrawlDelay <= 0 {
		return
	}
	domain := getDomain(rawURL)
	app.DomainDelayMu.Lock()
	now := time.Now()
	next := now
	if last, ok := app.DomainLastReq[domain]; ok && last.After(now) {
		next = last
	}
	app.DomainLastReq[domain] = next.Add(app.CrawlDelay)
	app.DomainDelayMu.Unlock()
	if sleep := time.Until(next); sleep > 0 {
		time.Sleep(sleep)
	}
}

// countRejection increments the app.Counter for one rejection reason.
func countRejection(reason string) {
	app.CrawlMu.Lock()
	app.RejectionCounts[reason]++
	app.CrawlMu.Unlock()
}

// rejectionSummary returns the rejection counters as log attributes.
func rejectionSummary() []interface{} {
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	keys := make([]string, 0, len(app.RejectionCounts))
	for k := range app.RejectionCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attrs := make([]interface{}, 0, len(keys)*2)
	for _, k := range keys {
		attrs = append(attrs, k, app.RejectionCounts[k])
	}
	return attrs
}

/***************************************************************************************************************
****************************************************************************************************************
* CSV FUNCTIONS ************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// csvInit creates the ranking CSV writer, appending when resuming.
func csvInit() error {
	var file *os.File
	var err error
	if app.ResumeEnabled {
		file, err = os.OpenFile("output/ranking-URL.csv", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	} else {
		file, err = os.Create("output/ranking-URL.csv")
	}
	if err != nil {
		return fmt.Errorf("cannot create ranking CSV: %w", err)
	}
	app.CSVFile = file
	app.CSVWriter = csv.NewWriter(file)
	app.CSVWriter.Comma = '\t'
	return nil
}

/***************************************************************************************************************
****************************************************************************************************************
* LINKS, DOWNLOAD AND CACHE FUNCTIONS **************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

/**** TYPES ****/

type aLink struct {
	URL    string
	Domain string
	Count  int
	Status int // 0 = pending, 1 = crawling, 2 = downloaded, 3 = failed, 4 = bootstrapping
}

type CachedData struct {
	Content string
	Links   []string
}

var commonSecondLevelSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true,
	"com.au": true, "net.au": true, "org.au": true, "co.nz": true,
	"co.jp": true, "co.in": true, "com.br": true, "com.mx": true,
	"co.za": true, "com.sg": true, "co.kr": true, "com.hk": true,
}

// proxyGet performs an HTTP GET with the shared client and optional proxy.
func proxyGet(urlLink string, printLog bool) (resp *http.Response, err error) {
	req, err := http.NewRequest("GET", urlLink, nil)
	if err != nil {
		return
	}
	if printLog {
		fmt.Printf("\nStart downloading")
	}
	resp, err = app.HTTPClient.Do(req)
	if printLog {
		fmt.Printf("\nEnd downloading")
	}
	return
}

// download fetches a page and returns its plain text and extracted links.
func download(urlLink string) (string, []string, error) {
	app.LogDownload.Printf("\n%s", urlLink)
	domain := getDomain(urlLink)
	if domainHadFailed(domain) {
		app.LogDownload.Printf("\tPreviously failed")
		return "", nil, errors.New("download: Previously failed")
	}
	app.LogDownload.Printf("\tRequested")

	resp, err := proxyGet(urlLink, true)
	if err != nil {
		app.LogDownload.Printf("\tHttp transport error:\n\t\t%s", err)
		domainReportFailed(domain)
		return "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		app.LogDownload.Printf("\tHTTP status: %d", resp.StatusCode)
		if resp.StatusCode >= 500 {
			domainReportFailed(domain)
		}
		resp.Body.Close()
		return "", nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" &&
		!strings.Contains(ct, "html") && !strings.Contains(ct, "text") && !strings.Contains(ct, "xml") {
		app.LogDownload.Printf("\tUnsupported content type: %s", ct)
		resp.Body.Close()
		return "", nil, fmt.Errorf("download: unsupported content type %s", ct)
	}

	// Read the response body.
	bodyBytes, err := ioutil.ReadAll(io.LimitReader(resp.Body, app.MaxDownloadBytes))
	if err != nil {
		app.LogDownload.Printf("\t%s%s", "Read error: ", err)
		return "", nil, err
	}
	resp.Body.Close() // must close
	resp.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))

	// Extract links from the HTML body.
	links := collectlinks.All(resp.Body) // Here we use the collectlinks package

	// Convert the HTML body to plain text.
	plain, err := html2text.FromString(string(bodyBytes), html2text.Options{PrettyTables: false})
	if err != nil {
		app.LogDownload.Printf("\t%s%s", "html2text.FromString fails: ", err)
		return "", nil, err
	}

	app.LogDownload.Printf("\t%s len(plain): %d len(links): %d", "Ok: ", len(plain), len(links))

	return plain, links, nil
}

// downloadCached returns a cached page or downloads and stores a new one.
func downloadCached(urlLink string) (content string, links []string, err error) {
	var cached CachedData
	cacheKey := "cache_" + urlLink

	if app.CacheStore.Exists(cacheKey) {
		value := app.CacheStore.Get(cacheKey)
		if value != "" {
			if err := json.Unmarshal([]byte(value), &cached); err != nil {
				app.Logger.Warn("discarding corrupt cache entry", "key", cacheKey, "error", err)
			} else {
				app.CacheHits++
				fmt.Printf(" [CACHE]")
				return cached.Content, cached.Links, nil
			}
		}
	}

	if app.RobotsPolicy != nil {
		if !app.RobotsPolicy.Allowed(urlLink) {
			countRejection("robots_blocked")
			logRejectedLink("robotsBlocked.log", urlLink)
			fmt.Printf(" [ROBOTS-BLOCKED]")
			return "", nil, errors.New("download: blocked by robots.txt")
		}
		app.RobotsPolicy.Wait(urlLink)
	}
	respectCrawlDelay(urlLink)

	start := time.Now()
	cached.Content, cached.Links, err = download(urlLink)
	app.DownloadMS += time.Since(start).Milliseconds()
	if err != nil {
		return "", nil, err
	}
	app.OnlineDownloads++
	encoded, marshalErr := json.Marshal(cached)
	if marshalErr != nil {
		app.Logger.Error("cannot marshal cache entry", "error", marshalErr)
	} else if setErr := app.CacheStore.Set(cacheKey, string(encoded)); setErr != nil {
		app.Logger.Error("cannot write cache entry", "error", setErr)
	}
	fmt.Printf(" [ONLINE]")
	return cached.Content, cached.Links, nil
}

// getDomain extracts the host name from a URL.
func getDomain(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		log.Println("getDomain error: ", err)
		return ""
	}

	// Find out main domain, ignore www. subdomains
	mainDomain := u.Hostname()
	mainDomain = reWww.ReplaceAllString(mainDomain, `$1`)

	return mainDomain
}

// getSecondLevelDomain returns the last two labels of a host.
func getSecondLevelDomain(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		log.Println("getSecondLevelDomain error: ", err)
		return ""
	}

	mainDomain := u.Hostname()
	labels := strings.Split(mainDomain, ".")
	if len(labels) > 2 {
		lastTwo := labels[len(labels)-2] + "." + labels[len(labels)-1]
		if commonSecondLevelSuffixes[lastTwo] && len(labels) > 2 {
			mainDomain = labels[len(labels)-3] + "." + lastTwo
		} else {
			mainDomain = lastTwo
		}
	}

	return mainDomain
}

// increaseDomainCounter records one more visit for a domain.
func increaseDomainCounter(domain string) {
	app.PoolMu.Lock()
	app.DomainCounter[domain]++
	app.PoolMu.Unlock()
}

// isBanned reports whether a link matches the configured blacklists.
func isBanned(link string, domain string) bool {
	if len(reBannedDomains.FindStringSubmatch(domain)) > 0 {
		return true
	}

	if len(reBannedTokens.FindStringSubmatch(link)) > 0 {
		return true
	}

	return false
}

// initRejectedLinkSets loads existing rejected links to deduplicate across runs.
func initRejectedLinkSets() {
	app.RejectMu.Lock()
	defer app.RejectMu.Unlock()
	for filename, set := range app.RejectedLinkSets {
		path := "./logs/" + filename
		if !iolib.FileExists(path) {
			continue
		}
		for _, line := range strings.Split(iolib.File2string(path), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if len(set) >= maxUniqueRejectedLinks {
				break
			}
			set[line] = struct{}{}
		}
	}
}

// logRejectedLink appends a rejected link to its log only once.
func logRejectedLink(filename, link string) {
	app.RejectMu.Lock()
	defer app.RejectMu.Unlock()
	link = strings.TrimSpace(link)
	set, ok := app.RejectedLinkSets[filename]
	if !ok {
		return
	}
	if _, seen := set[link]; seen {
		return
	}
	if len(set) >= maxUniqueRejectedLinks {
		return
	}
	set[link] = struct{}{}
	iolib.String2fileAppend(link, "./logs/"+filename)
}

// linkSeemsOk validates a link through the URL and profile filter chain.
func linkSeemsOk(l string, avoidCustomCheck bool) bool {

	if len(l) < 13 || len(l) > 256 {
		return false
	}

	_, err := url.ParseRequestURI(l)
	if err != nil {
		countRejection("invalid_url")
		logRejectedLink("linksNotOk.ParseRequestURI.log", l)
		return false
	}

	// Additional URL validator to avoid panic error cases like this:
	// http://(http://health.org/2010/04/17/
	matches := reURLStrict.FindAllStringSubmatch(l, -1)
	if len(matches) < 1 {
		countRejection("invalid_url")
		logRejectedLink("linksNotOk.additional.log", l)
		return false
	}

	// Check the URL against the profile whitelist.
	if !avoidCustomCheck {
		match := reLinkOk.FindStringSubmatch(l)
		if len(match) > 0 {
			app.Logger.Debug("link matched profile whitelist", "token", match[1], "url", l)
			return true
		} else {
			countRejection("off_topic_url")
			logRejectedLink("linksNotOk.log", l)
			return false
		}
	}

	return true
}

// getNextLink selects the highest-priority pending link from the pool.
func getNextLink() (int, string) {
	app.PoolMu.Lock()
	defer app.PoolMu.Unlock()
	maxi := 0
	lasti := 0
	maxURL := ""
	var priority, maxPriority float64

	if len(app.LPool) == 0 {
		return maxi, ""
	}

	for i, l := range app.LPool {
		if l.Status == 4 { // bootstrapping url
			fmt.Printf("\n\nFound bootstrapping url: %+v", l)
			// Set this item directly as next candidate
			maxi = i
			maxURL = l.URL
			maxPriority = 0
			break
		}

		priority = float64(l.Count) * float64(l.Count) / (float64(app.DomainCounter[l.Domain]) + 1.0)

		if l.Status == 0 && priority > maxPriority && !isBanned(l.URL, l.Domain) && linkSeemsOk(l.URL, true) {

			// Set this item as best candidate so far
			maxi = i
			maxURL = l.URL

			maxPriority = priority
		}
		lasti = i
	}
	fmt.Printf("* getNextLink() %d links on the pool. Found best link at %d position. Priority: %.03f\n", lasti, maxi, priority)

	if maxURL == "" {
		return maxi, ""
	}
	app.LPool[maxi].Status = 1
	app.DomainCounter[app.LPool[maxi].Domain]++
	app.StateDirty = true

	return maxi, maxURL
}

// addLink validates a link and adds or updates it in the pool.
func addLink(link string, avoidFilters bool) bool {
	domain := getDomain(link)

	if !linkSeemsOk(link, avoidFilters) { // Avoid links that do not pass keyword filter
		return false
	}

	if !avoidFilters {
		if domain == "" { // Avoid null and local urls
			return false
		}

		if isBanned(link, domain) { // Avoid banned domains
			return false
		}

		// Canonicalize the link by removing CGI parameters.
		link2 := reCgiParams.ReplaceAllString(link, "")
		if link2 != link {
			app.LogCGI.Printf("\n\n%s\n%s", link2, link)
			link = link2
		}
	}

	// Look for an existing entry in the link pool using the URL index.
	if i, ok := app.LinkIndex[link]; ok {
		app.LPool[i].Count++
		return true
	}

	// Register a new link in the pool.
	status := 0
	if avoidFilters {
		status = 4
	}
	app.LinkIndex[link] = len(app.LPool)
	app.StateDirty = true
	app.LPool = append(app.LPool, aLink{URL: link, Domain: domain, Count: 1, Status: status})

	return true
}

// linkBootstraping seeds the pool with the profile bootstrap URLs.
func linkBootstraping() {
	for _, l := range app.BootstrapLinks {
		addLink(l, true)
	}

	goDebug.Print("linkBootstraping", app.LPool)
}

// lPoolDump writes the link pool to a JSON file.
func lPoolDump() {
	jdata, err := json.MarshalIndent(app.LPool, "", " ")
	if err != nil {
		fmt.Println("error: ", err)
	}
	jsonFile, err := os.Create("./output/LPool.json")
	if err != nil {
		log.Println(err)
		return
	}
	defer jsonFile.Close()
	jsonFile.Write(jdata)
}

// domainCounterDump writes the per-domain visit counters to a JSON file.
func domainCounterDump() {
	jdata, err := json.MarshalIndent(app.DomainCounter, "", " ")
	if err != nil {
		fmt.Println("domainCounterDump error: ", err)
	}
	jsonFile, err := os.Create("./output/domainCounter.json")
	if err != nil {
		log.Println(err)
		return
	}
	defer jsonFile.Close()
	jsonFile.Write(jdata)
}

// domainReportFailed records a transport failure in memory and in the text log.
func domainReportFailed(domain string) {
	app.FailMu.Lock()
	app.FailedDomains[domain]++
	app.StateDirty = true
	app.FailMu.Unlock()
	iolib.String2fileAppend(domain+"\n", "./logs/domainFailed.log")
}

// domainHadFailed reports whether a domain exceeded the failure limit.
func domainHadFailed(domain string) bool {
	app.FailMu.Lock()
	defer app.FailMu.Unlock()
	return app.FailedDomains[domain] > 6
}

// initFailedDomains rebuilds the in-memory app.Counter from the text log.
func initFailedDomains() {
	app.FailMu.Lock()
	defer app.FailMu.Unlock()
	app.FailedDomains = make(map[string]int)
	path := "./logs/domainFailed.log"
	if !iolib.FileExists(path) {
		return
	}
	for _, line := range strings.Split(iolib.File2string(path), "\n") {
		domain := strings.TrimSpace(line)
		if domain != "" {
			app.FailedDomains[domain]++
		}
	}
}

/***************************************************************************************************************
****************************************************************************************************************
* TOKENIZER ****************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// tokenize splits text into tokens on non-alphanumeric characters.
func tokenize(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		// Split on any character that is not a letter or a number.
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// tokensCount returns the number of tokens in a text.
func tokensCount(text string) int {
	return len(tokenize(text))
}

// splitParagraphs splits text into non-empty paragraphs.
func splitParagraphs(text string) []string {
	ar := reParagraph.Split(text, -1)

	return ar
}

// rankingByKeywords scores text by unique keywords normalized by document length.
func rankingByKeywords(text string) float64 {
	return scoreWithRegex(text, reRankingKw)
}

// scoreWithRegex computes the legacy keyword score using a given matcher.
func scoreWithRegex(text string, re *regexp.Regexp) float64 {
	rr := re.FindAllStringSubmatch(text, -1)

	var uniqueK = make(map[string]int)

	for _, k := range rr {
		if len(k[1]) < 3 {
			continue
		}
		uniqueK[strings.ToLower(k[1])]++
	}

	var ks []string
	for kk := range uniqueK {
		ks = append(ks, kk)
	}

	rank := 100.0 * float64(len(ks)) / math.Sqrt(float64(1+len(tokenize(text))))

	return rank
}

const embeddingDim = 256

// tokenEmbedding hashes character trigrams into a fixed-size vector. This is
// a dependency-free approximation of word embeddings (the hashing trick) and
// captures morphological similarity between related terms.
func tokenEmbedding(token string) []float64 {
	vector := make([]float64, embeddingDim)
	word := strings.ToLower(token)
	if len(word) < 3 {
		word = " " + word + " "
	}
	for i := 0; i+3 <= len(word); i++ {
		h := fnv.New32a()
		h.Write([]byte(word[i : i+3]))
		vector[int(h.Sum32()%embeddingDim)]++
	}
	norm := 0.0
	for _, v := range vector {
		norm += v * v
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vector {
			vector[i] /= norm
		}
	}
	return vector
}

// cosineSimilarity computes the cosine of two embedding vectors.
func cosineSimilarity(a, b []float64) float64 {
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// profileEmbedding averages the embeddings of the profile ranking terms.
func profileEmbedding() []float64 {
	terms := regexTermList(app.RegexRankingKeywords)
	for _, alts := range app.Synonyms {
		terms = append(terms, alts...)
	}
	vector := make([]float64, embeddingDim)
	count := 0
	for _, term := range listUnique(terms) {
		if len(term) < 3 {
			continue
		}
		emb := tokenEmbedding(term)
		for i, v := range emb {
			vector[i] += v
		}
		count++
	}
	if count > 0 {
		for i := range vector {
			vector[i] /= float64(count)
		}
	}
	return vector
}

// semanticScore compares a document with the profile embedding.
func semanticScore(text string) float64 {
	profile := profileEmbedding()
	doc := make([]float64, embeddingDim)
	tokens := tokenize(text)
	count := 0
	for _, token := range tokens {
		if len(token) < 4 {
			continue
		}
		emb := tokenEmbedding(token)
		for i, v := range emb {
			doc[i] += v
		}
		count++
		if count >= 400 {
			break
		}
	}
	if count == 0 {
		return 0
	}
	for i := range doc {
		doc[i] /= float64(count)
	}
	return 100 * cosineSimilarity(profile, doc)
}

// rankingScore scores a document using the configured strategy.
func rankingScore(text string) float64 {
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	switch app.RankingStrategy {
	case "keywords":
		return rankingByKeywords(text)
	case "tfidf":
		return tfidfScore(text)
	case "semantic":
		return semanticScore(text)
	default:
		kw := 100.0 * float64(uniqueMatches(text, app.ReRankingExtended)) / math.Sqrt(float64(1+len(tokenize(text))))
		return (kw + tfidfScore(text) + semanticScore(text)) / 3
	}
}

// uniqueMatches counts distinct matched terms (including app.Synonyms) in a text.
func uniqueMatches(text string, re *regexp.Regexp) int {
	if re == nil {
		return 0
	}
	seen := make(map[string]bool)
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 && len(m[1]) >= 3 {
			seen[strings.ToLower(m[1])] = true
		}
	}
	return len(seen)
}

// tfidfScore returns the share of the document's information weight matched by
// the profile vocabulary. It is length-normalised by construction.
func tfidfScore(text string) float64 {
	tokens := tokenize(text)
	if len(tokens) == 0 || app.ReRankingExtended == nil {
		return 0
	}
	idf := func(term string) float64 {
		return math.Log(1 + float64(app.CorpusDocCount+1)/float64(1+app.CorpusDocFreq[strings.ToLower(term)]))
	}
	total, matched := 0.0, 0.0
	seen := make(map[string]bool)
	for _, token := range tokens {
		key := strings.ToLower(token)
		if seen[key] {
			continue
		}
		seen[key] = true
		total += idf(key)
	}
	for _, m := range app.ReRankingExtended.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 && len(m[1]) >= 3 {
			matched += idf(m[1])
		}
	}
	if total == 0 {
		return 0
	}
	return 100 * matched / total
}

// bestParagraph returns the highest-ranked paragraph capped at 2000 characters.
func bestParagraph(paragraphs []string) (bp string) {
	maxScore := 0.0
	s := 0.0
	for _, p := range paragraphs {
		s = rankingByKeywords(p)
		if s > maxScore {
			maxScore = s
			bp = p
		}
	}

	if len(bp) > 2000 {
		bp = bp[:1996] + " ..."
	}

	return
}

// lowercaseFilter converts tokens to lowercase.
func lowercaseFilter(tokens []string) []string {
	r := make([]string, len(tokens))
	for i, token := range tokens {
		r[i] = strings.ToLower(token)
	}
	return r
}

// stopWordsCount counts stopwords in a text.
func stopWordsCount(text string) int {
	matches := reStopWords.FindAllStringSubmatch(" "+text+" ", -1)

	return len(matches)
}

// stopWordsOnBorderCount counts stopwords at token boundaries.
func stopWordsOnBorderCount(text string) int {
	matchesLeft := reStopLeft.FindAllStringIndex(text, -1)
	matchesRight := reStopRight.FindAllStringIndex(text, -1)

	return len(matchesLeft) + len(matchesRight)
}

// lowRelevancyWordsOnBorderCount counts low-relevancy words at token boundaries.
func lowRelevancyWordsOnBorderCount(text string) int {
	matchesLeft := reLowRelLeft.FindAllStringIndex(text, -1)
	matchesRight := reStopRight.FindAllStringIndex(text, -1)

	return len(matchesLeft) + len(matchesRight)
}

// stopwordFilter removes stopwords from a text.
func stopwordFilter(text string) string {

	// Words must be separated by 2 spaces to regexp find all stopwords
	text = reNonWord.ReplaceAllString(text, `  `)
	text = fmt.Sprintf(" %s ", text)
	text2 := reStopwords.ReplaceAllString(text, ` `)
	text2 = strings.TrimSpace(text2)

	return text2
}

// stemmerFilter applies English stemming to tokens (currently disabled).
func stemmerFilter(tokens []string) []string {
	r := make([]string, len(tokens))
	for i, token := range tokens {
		r[i] = snowballeng.Stem(token, false)
	}
	return r
}

// analyze lowercases and filters tokens before frequency counting.
func analyze(t string) []string {
	tokens := tokenize(t)

	lTokens := lowercaseFilter(tokens)

	nonstopTokens := tokenize(stopwordFilter(strings.Join(lTokens[:], " ")))

	return nonstopTokens
}

type freq map[string]int

// add increments the frequency of a token.
func (f freq) add(t string) {
	app.Counter++
	for _, token := range analyze(t) {
		f[token]++
	}
}

type kv struct {
	Key   string
	Value int
}

type kvHeap []kv

func (h kvHeap) Len() int            { return len(h) }
func (h kvHeap) Less(i, j int) bool  { return h[i].Value < h[j].Value }
func (h kvHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *kvHeap) Push(x interface{}) { *h = append(*h, x.(kv)) }
func (h *kvHeap) Pop() interface{}   { old := *h; n := len(old); v := old[n-1]; *h = old[:n-1]; return v }

// topKFreq returns the k highest-frequency items without sorting the whole map.
func topKFreq(f freq, k int) []kv {
	if k <= 0 || len(f) == 0 {
		return nil
	}
	h := &kvHeap{}
	heap.Init(h)
	for key, value := range f {
		if h.Len() < k {
			heap.Push(h, kv{Key: key, Value: value})
		} else if value > (*h)[0].Value {
			heap.Pop(h)
			heap.Push(h, kv{Key: key, Value: value})
		}
	}
	result := make([]kv, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(kv)
	}
	return result
}

// rSortFreq sorts token frequencies from highest to lowest.
func rSortFreq(f freq) (ss []kv) {
	for k, v := range f {
		ss = append(ss, kv{k, v})
	}

	sort.Slice(ss, func(i, j int) bool {
		if ss[i].Value == ss[j].Value {
			return ss[i].Key > ss[j].Key
		}
		return ss[i].Value > ss[j].Value
	})

	return
}

// getKVkeys extracts the keys of a sorted frequency slice.
func getKVkeys(aMapArray []kv) []string {
	keys := make([]string, 0, len(aMapArray))
	for _, k := range aMapArray {
		keys = append(keys, k.Key)
	}

	return keys
}

// ngramsOf returns the n-grams of a text.
func ngramsOf(t string, n int) []string {
	var ngrams = []string{}

	// Regard each sentence as a paragraph
	t = strings.ReplaceAll(t, ".\n", "\n")
	t = strings.ReplaceAll(t, ". ", "\n")
	paragraphs := splitParagraphs(t)

	// Ignore repeated/similar sentences
	var uniqueSentenceSignatures = make(map[string]string)
	for _, p := range paragraphs {
		// remove stopwords from sentence
		p2 := stopwordFilter(p)

		// Find high frequency meaningful tokens
		sentenceTokenFreqs := make(freq)
		sentenceTokenFreqs.add(p2)
		sentenceTokenFreqsSorted := rSortFreq(sentenceTokenFreqs)

		// Build sentence signature w/ highest frequency meaningful tokens
		sentenceSignature := ""
		if len(sentenceTokenFreqsSorted) > 7 {
			sentenceSignature = fmt.Sprintf("%v", getKVkeys(sentenceTokenFreqsSorted[:7]))
		} else {
			sentenceSignature = fmt.Sprintf("%v", getKVkeys(sentenceTokenFreqsSorted))
		}

		// Useful sentences are stored with no duplication on uniqueSentenceSignatures value field
		if uniqueSentenceSignatures[sentenceSignature] == "" {
			uniqueSentenceSignatures[sentenceSignature] = p
		} else {
		}
	}

	// Words must be separated by 2 spaces to regexp find all matches
	// Precompiled regexes: reNonWord, reAvoidChars, reDoubleSpace, reWordMatch, reNGramsIgnore

	for _, p := range uniqueSentenceSignatures {
		// Prepare text
		p = reNonWord.ReplaceAllString(" "+p+" ", `$1$1`)
		p = reAvoidChars.ReplaceAllString(p, ` `)

		// Find bigrams
		bigramMatches := reWordMatch.FindAllStringSubmatch(p, -1)

		var previousToken = make(map[int]string)
		for _, aBigramFound := range bigramMatches {
			if len(tokenize(previousToken[n-1])) == n-1 {
				ngramCandidate := strings.TrimSpace(previousToken[n-1] + aBigramFound[0])

				// Remove double spaces
				ngramCandidate = reDoubleSpace.ReplaceAllString(ngramCandidate, `$1`)

				if lowRelevancyWordsOnBorderCount(ngramCandidate) == 0 { // avoid ngrams containing stopwords on left/right border
					if len(reNGramsIgnore.FindAllStringIndex(" "+ngramCandidate+" ", -1)) == 0 { // avoid ngrams to ignore
						ngrams = append(ngrams, ngramCandidate)
					}
				}
			}

			for i := n - 1; i > 0; i-- {
				previousToken[i] = previousToken[i-1] + aBigramFound[0]
			}
			previousToken[0] = aBigramFound[0]
		}
	}
	return ngrams
}

// ngramsFreqsOf counts n-grams in a text.
func ngramsFreqsOf(t string, n int) []kv {
	ngrams := ngramsOf(t, n)
	ngramsFreq := make(freq)
	for _, ngram := range ngrams {
		ngramsFreq[ngram]++
	}
	ngramsFreqSorted := rSortFreq(ngramsFreq)

	// Ignore residual low frequencies
	var limitedNgramsFreqSorted = []kv{}
	for counterNgramFreq, anNgramFreq := range ngramsFreqSorted {
		if counterNgramFreq > 100 || anNgramFreq.Value < 3 {
			break
		}
		limitedNgramsFreqSorted = append(limitedNgramsFreqSorted, kv{Key: anNgramFreq.Key, Value: anNgramFreq.Value})
	}

	return limitedNgramsFreqSorted
}

// kvSliceRemoveItem removes one element from a frequency slice.
func kvSliceRemoveItem(slice []kv, s int) []kv {
	fmt.Printf("kvSliceRemoveItem: len(slice): %d; s:%d\n", len(slice), s)
	fmt.Printf("kvSliceRemoveItem: slice1: %+v\n", slice)
	fmt.Printf("kvSliceRemoveItem: slice2: %+v\n\n", append(slice[:s], slice[s+1:]...))
	return append(slice[:s], slice[s+1:]...)
}

// When adding up all ngrams find out which sub-ngram or super-ngram must represent its class

// N-GRAM:  Coronavirus COVID Update FDA Issues 7
// N-GRAM:  Coronavirus COVID Update FDA Continues 3
// N-GRAM:  Coronavirus COVID Update FDA Revokes 2
// N-GRAM:  Coronavirus COVID Update FDA Authorizes 2
// N-GRAM:  Coronavirus COVID Update FDA 31 <=========================== bigger freq

// N-GRAM:  FDA actions on warning letters 16 <=========================== bigger n
// N-GRAM:  FDA actions on warning 16

// N-GRAM:  Food and Drug Administration’s 2
// N-GRAM:  Food and Drug Administration FDA 2
// N-GRAM:  Food and Drug Administration 12 <=========================== bigger freq

// N-GRAM:  FDA actions on food safety 3 <=========================== bigger n
// N-GRAM:  FDA actions on food 3

// Algorithm:
// 	#1.- check subsets. If any subset has higher frequency, ignore ngram
// 	#2.- check supersets. If any superset has equal or higher frequency, ignore ngram

// Problema:
// level 3; ngram: {Key:President Donald Trump Value:36}; subsetNgram: {Key:President Donald Value:40}
// level 4; ngram: {Key:Food and Drug Administration Value:84}; subsetNgram: {Key:Food and Drug Value:95}
// Algorithm tweaking:
// 	#1.- check subsets. If any subset has at least twofold higher frequency, ignore ngram

// N-GRAM:  Hahn M.D. 6
// N-GRAM:  Hahn M. 7
// 	#2.- check supersets. If any superset has at least 50% or higher frequency, ignore ngram

// ngramsFreqsOfAll counts n-grams for every size from nMax down to 2.
func ngramsFreqsOfAll(t string, nMax int) []kv { // All n's from nMax to 2 decreasingly
	var ngramsFreqs = []kv{}
	var ngramsLevel = make(map[int][]kv)
	var ngramsToIgnore = make(map[string]bool)
	var ngramsLevelOk = make(map[int][]kv)

	for n := nMax; n > 1; n-- {
		ngramsLevel[n] = ngramsFreqsOf(t, n)
	}
	for n := nMax; n > 1; n-- {
		for _, ngram := range ngramsLevel[n] { // find out ngrams to ignore
			// Ignore non-representative ngrams included in a class
			// Algorithm #2: check supersets
			if n < nMax {
				for _, supersetNgram := range ngramsLevel[n+1] { // loop supersets
					if strings.Contains(supersetNgram.Key, ngram.Key) {
						fmt.Printf("level %d; ngram: %+v; supersetNgram: %+v\n", n, ngram, supersetNgram)
						if supersetNgram.Value > ngram.Value/2 {
							// ignore this ngram
							// ngramsLevel[n] = kvSliceRemoveItem(ngramsLevel[n], ngramIndex)
							ngramsToIgnore[ngram.Key] = true
							fmt.Printf("*** REMOVED ***\n")
							break
						}
					}
				}
			}

			// Algorithm #1: check subsets
			if n > 2 {
				for _, subsetNgram := range ngramsLevel[n-1] { // loop sunsets
					if strings.Contains(ngram.Key, subsetNgram.Key) {
						fmt.Printf("level %d; ngram: %+v; subsetNgram: %+v\n", n, ngram, subsetNgram)
						if subsetNgram.Value > 2*ngram.Value {
							// ignore this ngram
							ngramsToIgnore[ngram.Key] = true
							fmt.Printf("*** REMOVED ***\n")
							break
						}
					}
				}
			}
		}
		for _, ngram := range ngramsLevel[n] { // rebuild ngramsLevel w/o ngrams to ignore
			if !ngramsToIgnore[ngram.Key] {
				ngramsLevelOk[n] = append(ngramsLevelOk[n], ngram)
			}
		}
		ngramsFreqs = append(ngramsFreqs, ngramsLevelOk[n]...)
	}

	return ngramsFreqs
}

/***************************************************************************************************************
****************************************************************************************************************
* MAIN *********************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// File loggers are initialized after the logs directory is created.

// initFileLoggers opens the log files. It must run after main creates ./logs.
func initFileLoggers() error {
	var err error
	f1, err := os.Create("./logs/download.log")
	if err != nil {
		return fmt.Errorf("download log: %w", err)
	}
	app.LogDownload = log.New(f1, "", 0)
	app.LogFiles = append(app.LogFiles, f1)
	f2, err := os.Create("./logs/cgi.log")
	if err != nil {
		return fmt.Errorf("cgi log: %w", err)
	}
	app.LogCGI = log.New(f2, "", 0)
	app.LogFiles = append(app.LogFiles, f2)
	f3, err := os.Create("./logs/domainFailed.log")
	if err != nil {
		return fmt.Errorf("domainFailed log: %w", err)
	}
	app.LogDomainFailed = log.New(f3, "", 0)
	app.LogFiles = append(app.LogFiles, f3)
	initFailedDomains()
	return nil
}

// closeFileLoggers flushes and closes the log files.
func closeFileLoggers() {
	for _, f := range app.LogFiles {
		if f != nil {
			f.Close()
		}
	}
	app.LogFiles = nil
}

// addLinksOf adds the links found in a page to the pool.
func addLinksOf(nextLink string, links []string) {
	linksAdded := 0
	limit := app.MaxLinksPerPage
	if limit <= 0 {
		limit = 200
	}
	// add links to pool
	for _, link := range links {
		if strings.Contains(getDomain(link), getSecondLevelDomain(nextLink)) {
		} else {
			if addLink(link, false) {
				linksAdded++
			}
			if strings.Contains(link, "wikipedia") {
				fmt.Printf("\n*************************************** %s [%s:%s]", link, getDomain(link), getSecondLevelDomain(nextLink))
			}
		}
		if linksAdded >= limit {
			break
		}
	}
	fmt.Printf("\n %d links found (%d added)", len(links), linksAdded)
}

// filterParagraphs drops non-English, markup-heavy and low-stopword paragraphs.
func filterParagraphs(paragraphs []string) []string {
	documentLanguage := ""
	for _, p := range paragraphs {
		if len(p) > 80 {
			documentLanguage = whatlanggo.Detect(p).Lang.String()
			break
		}
	}
	if documentLanguage == "" && len(paragraphs) > 0 {
		documentLanguage = whatlanggo.Detect(paragraphs[0]).Lang.String()
	}
	if documentLanguage != "" && documentLanguage != app.TargetLanguage {
		for i := range paragraphs {
			paragraphs[i] = ""
		}
		countRejection("non_english")
		return paragraphs
	}
	for i, p := range paragraphs {
		p2 := reRemoveUrls.ReplaceAllString(p, " ")
		p3 := reRemoveHtml.ReplaceAllString(p2, " ")
		p4 := reLongWords.ReplaceAllString(p3, " ")
		if len(reMarkup.FindAllStringSubmatch(p, -1)) > 0 {
			paragraphs[i] = ""
			continue
		}
		ratio := float64(stopWordsCount(p4)) / float64(len(tokenize(p4))+1)
		if ratio < 0.1 {
			countRejection("stopword_ratio")
			paragraphs[i] = ""
		} else {
			paragraphs[i] = p4
		}
	}
	return paragraphs
}

// buildCuratedContent joins paragraphs inside the stopword-ratio window.
func buildCuratedContent(paragraphs []string) string {
	content := ""
	for _, p := range paragraphs {
		if len(p) < 200 {
			continue
		}
		ratio := float64(stopWordsCount(p)) / float64(tokensCount(p)+1)
		if ratio < 0.1 || ratio > 0.38 {
			continue
		}
		content += "\n" + p
	}
	return content
}

// documentSignature returns the most frequent meaningful tokens of a document.
func documentSignature(curatedContent string) (string, []kv) {
	fDoc := make(freq)
	fDoc.add(curatedContent)
	fDoc["the"] = 0
	sorted := rSortFreq(fDoc)
	if len(sorted) > 7 {
		return fmt.Sprintf("%v", getKVkeys(sorted[:7])), sorted
	}
	return fmt.Sprintf("%v", getKVkeys(sorted)), sorted
}

// isUniqueDocument registers a signature and reports whether it was new.
func isUniqueDocument(signature, url string) bool {
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	if app.UniqueSignature[signature] == "" {
		app.UniqueSignature[signature] = url
		app.StateDirty = true
		return true
	}
	countRejection("duplicate")
	app.Logger.Debug("document rejected", "reason", "duplicate", "url", url, "duplicate_of", app.UniqueSignature[signature])
	return false
}

// writeDocument appends the curated text and its ranking row to the outputs.
func writeDocument(url, content, signature, bestParagraph string, score float64) error {
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	iolib.String2fileAppend(url+"\n"+content+"----\n\n\n\n", "./logs/corpusCuratedText.log")
	if err := app.CSVWriter.Write([]string{fmt.Sprintf("%.02f", score), fmt.Sprintf("%d", len(tokenize(content))), signature, url, bestParagraph}); err != nil {
		return err
	}
	app.CSVWriter.Flush()
	return nil
}

// pruneTerminalLinks removes finished links from the pool while keeping counters.
func pruneTerminalLinks() {
	app.PoolMu.Lock()
	defer app.PoolMu.Unlock()
	kept := make([]aLink, 0, len(app.LPool))
	for _, link := range app.LPool {
		if link.Status == 2 || link.Status == 3 {
			continue
		}
		kept = append(kept, link)
	}
	if len(kept) == len(app.LPool) {
		return
	}
	app.LPool = kept
	app.LinkIndex = make(map[string]int, len(app.LPool))
	for i, link := range app.LPool {
		app.LinkIndex[link.URL] = i
	}
}

// capAuxiliarySets bounds memory used by deduplication structures.
func capAuxiliarySets() {
	const cap = 100000
	app.CrawlMu.Lock()
	if len(app.UniqueSignature) > cap {
		app.UniqueSignature = make(map[string]string)
	}
	app.CrawlMu.Unlock()
	app.RejectMu.Lock()
	for _, set := range app.RejectedLinkSets {
		if len(set) > cap {
			for key := range set {
				delete(set, key)
				if len(set) <= cap/2 {
					break
				}
			}
		}
	}
	app.RejectMu.Unlock()
}

// dumpFrequencies writes frequency files and JSON dumps every 50 links, or when forced.
func dumpFrequencies(numLinksProcessed int, force bool) {
	if !force && numLinksProcessed%50 != 0 {
		return
	}
	pruneTerminalLinks()
	capAuxiliarySets()
	saveCrawlState()
	app.Logger.Info("progress", "processed", numLinksProcessed, "accepted", app.AcceptedDocs,
		"rejections", rejectionSummary())

	app.CrawlMu.Lock()
	freqCopy := make(freq, len(app.CorpusFreqs))
	for k, v := range app.CorpusFreqs {
		freqCopy[k] = v
	}
	app.CrawlMu.Unlock()
	allSorted := rSortFreq(freqCopy)
	top := topKFreq(freqCopy, 100)
	output := ""
	for _, item := range allSorted {
		output += fmt.Sprintf("%d %s %s %d\n", item.Value, item.Key, "none", 0)
	}
	iolib.String2file(output, "./output/corpusFrequencies.txt")

	withoutEnglish := make(freq)
	if len(top) > 0 {
		theValue := freqCopy["the"]
		if theValue > 0 {
			scale := float64(1+corpusfreqlib.Freq("the")) / float64(theValue)
			for _, item := range top {
				withoutEnglish[item.Key] = item.Value - int(20.0*float64(1+corpusfreqlib.Freq(item.Key))/scale)
			}
		}
	}
	withoutSorted := rSortFreq(withoutEnglish)
	output = ""
	for _, item := range withoutSorted {
		output += fmt.Sprintf("%d %s\n", item.Value, item.Key)
	}
	iolib.String2file(output, "./output/corpusNoEngFrequencies.txt")
	lPoolDump()
	domainCounterDump()
}

// notifyIndexer optionally submits an accepted URL to an external indexer.
func notifyIndexer(rawURL string) {
	if len(app.AddURL) == 0 {
		return
	}
	target := app.AddURL + url.QueryEscape(rawURL)
	fmt.Printf("\nrequestIndexUrl: %s", target)
	go func() {
		if _, err := proxyGet(target, false); err != nil {
			fmt.Printf("\n\nHttp transport error:\n\t\t%s", err)
		}
	}()
}

// doNextLink processes the next queued link and reports whether crawling should continue.
func doNextLink(numLinksProcessed int) bool {
	maxi, nextLink := getNextLink()
	linkSeemsOk(nextLink, false)
	if nextLink == "" {
		fmt.Println("* No more links available in the pool")
		fmt.Println(maxi)
		return false
	}

	app.PoolMu.Lock()
	prevState := app.LPool[maxi].Status
	app.PoolMu.Unlock()
	if app.StopRequested.Load() {
		return false
	}
	fmt.Printf("\n\n* Downloading url: %s", nextLink)

	content, links, err := downloadCached(nextLink)
	if err != nil {
		app.LPool[maxi].Status = 3
		fmt.Println("\nDownload error: ", strings.Trim(err.Error(), "\n"))
	} else {
		app.LPool[maxi].Status = 2
	}

	if prevState == 4 && app.LPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}

	paragraphs := filterParagraphs(splitParagraphs(content))
	best := bestParagraph(paragraphs)
	curated := buildCuratedContent(paragraphs)
	docLen := len(tokenize(curated))

	if docLen > app.MaxDocLen {
		countRejection("too_long")
		app.Logger.Debug("document rejected", "reason", "too_long", "url", nextLink, "tokens", docLen)
		return true
	}
	if docLen < app.MinDocLen {
		countRejection("too_short")
		app.Logger.Debug("document rejected", "reason", "too_short", "url", nextLink, "tokens", docLen)
		return true
	}

	signature, sortedFreqs := documentSignature(curated)
	if !isUniqueDocument(signature, nextLink) {
		return true
	}

	score := rankingScore(curated)
	if score < app.ScoreThreshold {
		countRejection("low_relevancy")
		app.Logger.Debug("document rejected", "reason", "low_relevancy", "url", nextLink, "score", score)
		return true
	}

	if len(sortedFreqs) > 0 {
		ratio := float64(sortedFreqs[0].Value) / float64(1+docLen)
		if ratio > 0.1 || ratio < 0.002 {
			countRejection("token_distribution")
			app.Logger.Debug("document rejected", "reason", "token_distribution", "url", nextLink)
			return true
		}
	}

	if err := writeDocument(nextLink, curated, signature, best, score); err != nil {
		countRejection("output_error")
		app.Logger.Error("cannot write document", "url", nextLink, "error", err)
		return true
	}
	indexDocument(nextLink, curated)
	app.CrawlMu.Lock()
	app.AcceptedDocs++
	app.StateDirty = true
	app.CorpusDocCount++
	for _, token := range tokenize(curated) {
		app.CorpusDocFreq[strings.ToLower(token)]++
	}
	app.CorpusFreqs.add(curated)
	app.StateDirty = true
	app.CrawlMu.Unlock()

	dumpFrequencies(numLinksProcessed, false)
	notifyIndexer(nextLink)

	if prevState == 0 && app.LPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}
	return true
}

// yamlInitGeneral loads the shared crawler.yaml configuration.
func yamlInitGeneral() error {
	if app.ConfigFile != "" {
		viper.SetConfigFile(app.ConfigFile)
	} else {
		viper.SetConfigName("crawler")
		viper.AddConfigPath(".")
	}
	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		return fmt.Errorf("fatal error config file: %w", err)
	}
	app.RegexBannedDomains = stringlib.RmNewLines(viper.GetString("regexBannedDomains"))
	app.RegexLinkBannedTokens = stringlib.RmNewLines(viper.GetString("regexLinkBannedTokens"))
	app.EngStopWordsWOthe = stringlib.RmNewLines(viper.GetString("engStopWordsWOthe"))
	app.EngStopWords = `the|` + app.EngStopWordsWOthe
	app.EngLowRelevancyWords = `|` + stringlib.RmNewLines(viper.GetString("engLowRelevancyWords"))
	app.RegexStopwords = `(?i)\W([0-9]+|.|..|` + app.EngStopWordsWOthe + app.EngLowRelevancyWords + `|` + stringlib.RmNewLines(viper.GetString("specialStopwords")) + `)\W`
	app.DownloadTimeout = time.Duration(viper.GetInt("downloadTimeout")) * time.Second
	if maxDownloadMB := viper.GetInt("maxDownloadMB"); maxDownloadMB > 0 {
		app.MaxDownloadBytes = int64(maxDownloadMB) << 20
	}
	app.AddURL = strings.TrimSpace(viper.GetString("addUrl"))
	app.CacheBackend = strings.TrimSpace(viper.GetString("cacheBackend"))
	if app.CacheBackend == "" {
		app.CacheBackend = storelib.BackendLocal
	}
	app.CachePath = strings.TrimSpace(viper.GetString("cachePath"))
	if app.CachePath == "" {
		app.CachePath = "./cache/cache.db"
	}
	app.LogLevel = strings.TrimSpace(viper.GetString("logLevel"))
	if app.LogLevel == "" {
		app.LogLevel = "info"
	}
	app.RespectRobots = true
	if viper.IsSet("respectRobots") {
		app.RespectRobots = viper.GetBool("respectRobots")
	}
	app.UserAgent = strings.TrimSpace(viper.GetString("userAgent"))
	if app.UserAgent == "" {
		app.UserAgent = "goCrawler/1.0 (+https://github.com/computerphysicslab/goCrawler)"
	}

	fmt.Printf("\n\nlogLevel: %s", app.LogLevel)
	fmt.Printf("\nrespectRobots: %v", app.RespectRobots)
	fmt.Printf("\nuserAgent: %s", app.UserAgent)
	fmt.Printf("\n\ncacheBackend: %s", app.CacheBackend)
	fmt.Printf("\ncachePath: %s", app.CachePath)
	fmt.Printf("\n\nregexBannedDomains: %s", app.RegexBannedDomains)
	fmt.Printf("\n\nregexLinkBannedTokens: %s", app.RegexLinkBannedTokens)
	fmt.Printf("\n\nengStopWordsWOthe: %s", app.EngStopWordsWOthe)
	fmt.Printf("\n\nengStopWords: %s", app.EngStopWords)
	fmt.Printf("\n\nengLowRelevancyWords: %s", app.EngLowRelevancyWords)
	fmt.Printf("\n\nregexStopwords: %s", app.RegexStopwords)
	fmt.Printf("\n\ndownloadTimeout: %+v", app.DownloadTimeout)
	fmt.Printf("\n\naddUrl: %s", app.AddURL)
	return nil
}

// yamlInitProxy loads the optional proxy.yaml settings.

func yamlInitProxy() error {
	if !iolib.FileExists("./proxy.yaml") {
		return nil
	}
	viper.SetConfigName("proxy") // name of config file (without extension)
	viper.AddConfigPath(".")     // look for config in the working directory
	err := viper.ReadInConfig()  // Find and read the config file
	if err != nil {              // Handle errors reading the config file
		return fmt.Errorf("fatal error config file: %w", err)
	}

	app.ProxyHost = viper.GetString("proxyHost")
	app.ProxyUser = viper.GetString("proxyUser")
	app.ProxyPass = viper.GetString("proxyPass")

	fmt.Printf("\n\nproxyHost: %s", app.ProxyHost)
	fmt.Printf("\n\nproxyUser: %s", app.ProxyUser)
	return nil
}

// yamlInitSpecific loads the profile selected on the command line.

func yamlInitSpecific() error {
	vp := viper.New()
	vp.SetConfigName(app.ProfileName)
	if iolib.FileExists("./profiles/" + app.ProfileName + ".yaml") {
		vp.AddConfigPath("./profiles")
	} else {
		vp.AddConfigPath(".")
	}
	if err := vp.ReadInConfig(); err != nil {
		return fmt.Errorf("fatal error config file: %w", err)
	}
	fmt.Printf("\n\nprofile file: %s", vp.ConfigFileUsed())
	app.CuratedDomains = stringlib.RmNewLines(vp.GetString("curatedDomains"))
	app.RegexLinkOk = `(?i)^https*://.*(` + stringlib.RmNewLines(vp.GetString("linkOk")) + `|` + app.CuratedDomains + `)`
	app.RegexRankingKeywords = stringlib.RmNewLines(vp.GetString("regexRankingKeywords"))
	app.BootstrapLinks = vp.GetStringSlice("bootstrapingLinks")
	app.MinDocLen = vp.GetInt("minDocLen")
	app.MaxDocLen = vp.GetInt("maxDocLen")
	app.ScoreThreshold = float64(vp.GetInt("scoreThreshold"))
	app.MaxLinksPerPage = vp.GetInt("maxLinksPerPage")
	if ms := vp.GetInt("crawlDelayMs"); ms > 0 {
		app.CrawlDelay = time.Duration(ms) * time.Millisecond
	}
	language := strings.TrimSpace(vp.GetString("language"))
	if language != "" {
		app.TargetLanguage = language
	}
	app.MinLanguageConfidence = vp.GetFloat64("minLanguageConfidence")
	app.RankingStrategy = strings.ToLower(strings.TrimSpace(vp.GetString("rankingStrategy")))
	if app.RankingStrategy == "" {
		app.RankingStrategy = "hybrid"
	}
	app.Synonyms = parseSynonyms(vp.GetString("synonyms"))
	buildRankingRegex()
	if app.MaxLinksPerPage <= 0 {
		app.MaxLinksPerPage = 200
	}

	fmt.Printf("\n\nprofileName: %s", app.ProfileName)
	fmt.Printf("\n\ncuratedDomains: %s", app.CuratedDomains)
	fmt.Printf("\n\nregexLinkOk: %s", app.RegexLinkOk)
	fmt.Printf("\n\nregexRankingKeywords: %s", app.RegexRankingKeywords)
	fmt.Printf("\n\nbootstrapingLinks: %+v", app.BootstrapLinks)
	return nil
}

// initSearchIndex opens the SQLite FTS5 index for the curated corpus.
func initSearchIndex() error {
	if app.SearchDB != nil {
		return nil
	}
	db, err := sql.Open("sqlite", app.CachePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS corpus_fts USING fts5(url UNINDEXED, text)`); err != nil {
		db.Close()
		return err
	}
	app.SearchDB = db
	return nil
}

// indexDocument adds or replaces a document in the full-text index.
func indexDocument(docURL, text string) {
	if app.SearchDB == nil {
		return
	}
	app.SearchDB.Exec(`DELETE FROM corpus_fts WHERE url = ?`, docURL)
	if _, err := app.SearchDB.Exec(`INSERT INTO corpus_fts(url, text) VALUES(?, ?)`, docURL, text); err != nil {
		app.Logger.Debug("cannot index document", "url", docURL, "error", err)
	}
}

// runSearch queries the curated corpus and prints matching documents.
func runSearch(query string) error {
	if err := initSearchIndex(); err != nil {
		return err
	}
	terms := []string{}
	for _, word := range strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len(word) > 1 {
			terms = append(terms, `"`+word+`"`)
		}
	}
	if len(terms) == 0 {
		return fmt.Errorf("no searchable terms in query: %q", query)
	}
	match := strings.Join(terms, " AND ")
	rows, err := app.SearchDB.Query(`SELECT url, snippet(corpus_fts, 1, '[', ']', ' ... ', 16)
		FROM corpus_fts WHERE corpus_fts MATCH ? ORDER BY rank LIMIT 20`, match)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var docURL, snippet string
		if err := rows.Scan(&docURL, &snippet); err != nil {
			return err
		}
		found++
		fmt.Printf("\n[%d] %s\n%s\n", found, docURL, snippet)
	}
	if found == 0 {
		fmt.Println("No matches for:", query)
	}
	return nil
}

const appVersion = "1.7.1"

// parseCLI parses the command line while keeping positional-profile compatibility.
func parseCLI() error {
	args := make([]string, 0, len(os.Args)-1)
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
			if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				arg += "=" + os.Args[i+1]
				i++
			}
		}
		args = append(args, arg)
	}
	for _, arg := range args {
		switch {
		case arg == "--selftest", arg == "-h", arg == "--help", arg == "--resume", arg == "-resume":
			continue
		case arg == "-gui", arg == "--gui":
			appGUI = true
		case strings.HasPrefix(arg, "-gui-port="):
			port, err := strconv.Atoi(strings.SplitN(arg, "=", 2)[1])
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("invalid -gui-port: %s", arg)
			}
			app.GUIPort = port
		case strings.HasPrefix(arg, "-config="), strings.HasPrefix(arg, "--config="):
			app.ConfigFile = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-profile="), strings.HasPrefix(arg, "--profile="):
			app.ProfileName = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-backend="), strings.HasPrefix(arg, "--backend="):
			app.CacheBackend = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-cache="), strings.HasPrefix(arg, "--cache-path="):
			app.CachePath = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-log-level="), strings.HasPrefix(arg, "--log-level="):
			app.LogLevel = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-max-links="):
			v, err := strconv.Atoi(strings.SplitN(arg, "=", 2)[1])
			if err != nil {
				return fmt.Errorf("invalid -max-links: %w", err)
			}
			app.MaxLinksPerPage = v
		case strings.HasPrefix(arg, "-workers="):
			v, err := strconv.Atoi(strings.SplitN(arg, "=", 2)[1])
			if err != nil || v < 1 || v > 32 {
				return fmt.Errorf("invalid -workers (1..32): %s", arg)
			}
			app.CLIWorkers = v
		case strings.HasPrefix(arg, "-query="), strings.HasPrefix(arg, "--query="):
			app.CLIQuery = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-generate-profile="):
			app.CLIGenerate = strings.SplitN(arg, "=", 2)[1]
		case arg == "-dry-run", arg == "--dry-run":
			app.CLIDryRun = true
		case arg == "-explore", arg == "--explore":
			app.CLIExplore = true
		case arg == "-version", arg == "--version":
			app.ShowVersion = true
		case arg == "-license", arg == "--license":
			app.ShowLicense = true
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag: %s", arg)
		default:
			if app.ProfileName == "" {
				app.ProfileName = arg
			}
		}
	}
	if app.ShowVersion || app.ShowLicense {
		if app.ShowVersion {
			fmt.Printf("goCrawler %s\n", appVersion)
		}
		if app.ShowLicense {
			fmt.Println("MIT License - Copyright (c) 2020-2026 Juan Ignacio Perez Sacristan")
		}
		return errStop
	}
	if app.ProfileName == "" && app.CLIQuery == "" && !app.CLIExplore && app.CLIGenerate == "" && !appGUI {
		return fmt.Errorf("profile argument required")
	}
	return nil
}

// errStop signals that a informational flag was handled and the program should exit cleanly.
var appGUI bool

var errStop = errors.New("stop")

// exploreCorpus prints corpus statistics and the most frequent topic terms.
func exploreCorpus() error {
	path := "./logs/corpusCuratedText.log"
	if !iolib.FileExists(path) {
		return fmt.Errorf("no corpus at %s", path)
	}
	urls := 0
	for _, line := range strings.Split(iolib.File2string(path), "\n") {
		if strings.HasPrefix(line, "http") {
			urls++
		}
	}
	fmt.Printf("Curated documents: %d\n", urls)
	freqPath := "./output/corpusNoEngFrequencies.txt"
	if !iolib.FileExists(freqPath) {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(iolib.File2string(freqPath)), "\n")
	limit := 20
	if len(lines) < limit {
		limit = len(lines)
	}
	fmt.Println("Top topic terms:")
	for i := 0; i < limit; i++ {
		fmt.Println(" ", lines[i])
	}
	return nil
}

// generateProfile writes a starter profile from a seed URL.
func generateProfile(seed, output string) error {
	u, err := url.Parse(seed)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid seed URL: %s", seed)
	}
	domain := getDomain(seed)
	terms := regexTermList(`(?i)\W(images?|stack|stacking|calibration|astrometry|plate|noise|gaia|siril)\W`)
	for _, part := range strings.FieldsFunc(strings.ToLower(u.Path), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len(part) > 3 {
			terms = append(terms, part)
		}
	}
	terms = listUnique(terms)
	content := fmt.Sprintf("# Generated profile for %s\ncuratedDomains: |\n  %s\n\nlinkOk: |\n  %s\n\nregexRankingKeywords: |\n  (?i)\\W(%s)\\W\n\nminDocLen: 50\nmaxDocLen: 100000\nscoreThreshold: 10\nrankingStrategy: hybrid\n\nbootstrapingLinks:\n- %q\n",
		seed, strings.ReplaceAll(domain, ".", `\.`), strings.Join(terms, "|"), strings.Join(terms, "|"), seed)
	if err := os.WriteFile(output, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Println("Profile written to", output)
	return nil
}

const guiHTML = `<!doctype html><html><head><meta charset="utf-8"><title>goCrawler Workbench</title>
<style>body{font-family:system-ui;margin:0;background:#10151c;color:#dfe7ee}header{padding:12px 20px;background:#1b2733}nav button{background:none;border:0;color:#9fb3c8;padding:10px 16px;cursor:pointer;font-size:15px}nav button.on{color:#fff;border-bottom:2px solid #4fc3f7}main{padding:18px 22px}.card{background:#1b2733;border-radius:8px;padding:14px;margin-bottom:14px}textarea,input,select{width:100%;background:#0e1620;color:#dfe7ee;border:1px solid #2c3e50;border-radius:4px;padding:8px;box-sizing:border-box;margin:6px 0}button.act{background:#2b6cb0;color:#fff;border:0;border-radius:4px;padding:8px 14px;cursor:pointer}button.act:disabled{opacity:.4;cursor:not-allowed;background:#455a6b}pre{background:#0e1620;padding:10px;border-radius:4px;white-space:pre-wrap;max-height:380px;overflow:auto}table{width:100%;border-collapse:collapse}td,th{padding:6px;border-bottom:1px solid #2c3e50;text-align:left}.row{display:flex;gap:12px}.row>*{flex:1}.muted{color:#8fa3b8}</style></head><body>
<header><b>goCrawler Workbench</b> <span class="muted" id="status">ready</span></header>
<nav><button class="on" data-tab="dash">Dashboard</button><button data-tab="prof">Profiles</button><button data-tab="corpus">Corpus</button><button data-tab="keys">Keyword lab</button></nav><main>
<section id="dash"><div class="card"><div class="row"><div>Pages visited<div id="s-visits">0</div></div><div>Cache hits<div id="s-cache">0</div></div><div>Online<div id="s-online">0</div></div><div>Download ms<div id="s-ms">0</div></div><div>Accepted<div id="s-accepted">0</div></div><div>Not accepted<div id="s-rejected">0</div></div><div>Crawls running<div id="s-running">0</div></div></div></div>
<div class="card"><h3>Why pages were not accepted</h3><div id="reasons"></div><p class="muted">Filter decisions are counted per paragraph/link, so totals can exceed the number of pages.</p></div>
<div class="card"><h3>Run a crawl</h3><input id="run-profile" placeholder="profile name"><button id="btn-start" class="act" onclick="startCrawl()">Start</button> <button id="btn-stop" class="act" onclick="stopCrawl()" disabled>Stop</button><pre id="run-out"></pre></div></section>
<section id="prof" hidden><div class="card"><select id="p-list" onchange="loadProfile()"></select><textarea id="p-text" rows="22"></textarea><button class="act" onclick="saveProfile()">Save profile</button></div></section>
<section id="corpus" hidden><div class="card"><input id="q" placeholder="FTS5 query"><button class="act" onclick="search()">Search</button><div id="docs"></div></div></section>
<section id="keys" hidden><div class="card"><p class="muted">Extract topic keywords from the curated corpus and feed them back into a profile.</p><input id="k-profile" placeholder="profile name"><button class="act" onclick="extract()">Extract</button><pre id="k-out"></pre><button class="act" onclick="applyKeywords()">Apply to profile</button></div></section>
</main><script>
const $=id=>document.getElementById(id);
document.querySelectorAll('nav button').forEach(b=>b.onclick=()=>{document.querySelectorAll('nav button').forEach(x=>x.classList.remove('on'));b.classList.add('on');['dash','prof','corpus','keys'].forEach(t=>$(t).hidden=t!==b.dataset.tab)});
async function json(u,o){const r=await fetch(u,o);return r.json()}
async function stats(){const s=await json('/api/stats');$('s-visits').textContent=s.visits;$('s-cache').textContent=s.cache;$('s-online').textContent=s.online;$('s-ms').textContent=s.download_ms;$('s-accepted').textContent=s.accepted;$('s-rejected').textContent=s.not_accepted;$('s-running').textContent=s.running_count;$('btn-start').disabled=s.running;$('btn-stop').disabled=!s.running;$('status').textContent=s.error?('error: '+s.error):(s.running?('running ('+s.running_count+')'):'ready');const entries=Object.entries(s.rejections||{}).sort((a,b)=>b[1]-a[1]);$('reasons').innerHTML=entries.length?('<table><tr><th>Reason</th><th>Count</th></tr>'+entries.map(e=>'<tr><td>'+e[0]+'</td><td>'+e[1]+'</td></tr>').join('')+'</table>'):'<span class=muted>No filter decisions yet</span>'}
async function profiles(){const p=await json('/api/profiles');$('p-list').innerHTML=p.map(x=>'<option>'+x+'</option>').join('');loadProfile()}
async function loadProfile(){const n=$('p-list').value;if(!n)return;const d=await json('/api/profile?name='+encodeURIComponent(n));$('p-text').value=d.content;$('k-profile').value=n;$('run-profile').value=n}
async function saveProfile(){const n=$('p-list').value;await fetch('/api/profile',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:n,content:$('p-text').value})});$('status').textContent='saved'}
async function search(){const d=await json('/api/search?q='+encodeURIComponent($('q').value));$('docs').innerHTML=d.results.map(x=>'<div class="card"><b>'+x.url+'</b><pre>'+x.snippet+'</pre><button class="act" onclick="copyText(this)">Copy</button></div>').join('')}
async function extract(){const n=$('k-profile').value||$('p-list').value;const d=await json('/api/keywords?profile='+encodeURIComponent(n));$('k-out').textContent=d.keywords.join(' ')}
async function applyKeywords(){const n=$('k-profile').value;const kws=$('k-out').textContent.split(/\s+/).filter(Boolean);const r=await fetch('/api/keywords/apply',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile:n,keywords:kws})});$('status').textContent=(await r.json()).status}
async function startCrawl(){const r=await fetch('/api/crawl',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile:$('run-profile').value})});$('run-out').textContent=JSON.stringify(await r.json());stats()}
async function stopCrawl(){await fetch('/api/crawl/stop',{method:'POST'});$('status').textContent='stopping';stats()}
function copyText(b){navigator.clipboard.writeText(b.previousElementSibling.textContent)}
setInterval(stats,1500);stats();profiles()
</script></body></html>`

var guiStopwords = map[string]bool{
	"with": true, "this": true, "that": true, "from": true, "will": true, "each": true,
	"when": true, "have": true, "your": true, "their": true, "into": true, "than": true,
	"about": true, "them": true, "then": true, "only": true, "very": true, "more": true,
	"most": true, "some": true, "such": true, "also": true, "just": true, "were": true,
	"was": true, "are": true, "the": true, "and": true, "for": true, "you": true,
	"its": true, "not": true, "but": true, "can": true, "all": true, "one": true,
}

var guiCrawling atomic.Bool

// launchGUICrawl runs a crawl inside the GUI process so its metrics are live.
func launchGUICrawl(profile string) error {
	if !iolib.FileExists("./profiles/" + filepath.Base(profile) + ".yaml") {
		return fmt.Errorf("profile not found: %s", profile)
	}
	if !guiCrawling.CompareAndSwap(false, true) {
		return fmt.Errorf("a crawl is already running")
	}
	go func() {
		defer guiCrawling.Store(false)
		app = newCrawler()
		app.ProfileName = profile
		app.GUILastError = ""
		startIndex, cleanup, err := setupRuntime(profile)
		if err != nil {
			app.GUILastError = err.Error()
			return
		}
		defer cleanup()
		final := crawlLoop(startIndex)
		reportResults(final)
	}()
	return nil
}

// guiHandler builds the HTTP handler for the workbench; exported for tests.
func guiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, guiHTML)
	})
	mux.HandleFunc("/api/profiles", func(w http.ResponseWriter, r *http.Request) {
		names := []string{}
		files, _ := filepath.Glob("profiles/*.yaml")
		for _, f := range files {
			names = append(names, strings.TrimSuffix(filepath.Base(f), ".yaml"))
		}
		writeJSON(w, names)
	})
	mux.HandleFunc("/api/profile", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			name := filepath.Base(r.URL.Query().Get("name"))
			if name == "" || name == "." {
				http.Error(w, "profile required", 400)
				return
			}
			path := "profiles/" + name + ".yaml"
			b, err := os.ReadFile(path)
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
			writeJSON(w, map[string]string{"name": name, "content": string(b)})
		case http.MethodPost:
			var body struct{ Name, Content string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			body.Name = filepath.Base(body.Name)
			path := "profiles/" + body.Name + ".yaml"
			if body.Name == "" || body.Name == "." {
				http.Error(w, "profile required", 400)
				return
			}
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(body.Content), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Rename(tmp, path); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]string{"status": "saved"})
		}
	})
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		visits := app.CacheHits + app.OnlineDownloads
		running := guiCrawling.Load()
		runningCount := 0
		if running {
			runningCount = 1
		}
		app.CrawlMu.Lock()
		reasons := make(map[string]int, len(app.RejectionCounts))
		for reason, count := range app.RejectionCounts {
			reasons[reason] = count
		}
		app.CrawlMu.Unlock()
		writeJSON(w, map[string]interface{}{
			"visits":        visits,
			"cache":         app.CacheHits,
			"online":        app.OnlineDownloads,
			"download_ms":   app.DownloadMS,
			"accepted":      app.AcceptedDocs,
			"not_accepted":  visits - app.AcceptedDocs,
			"running":       running,
			"running_count": runningCount,
			"rejections":    reasons,
			"error":         app.GUILastError,
		})
	})
	mux.HandleFunc("/api/documents", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, corpusDocuments(20))
	})
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			writeJSON(w, map[string]interface{}{"results": []string{}})
			return
		}
		if err := initSearchIndex(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		terms := []string{}
		for _, word := range strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
			if len(word) > 1 {
				terms = append(terms, `"`+word+`"`)
			}
		}
		rows, err := app.SearchDB.Query(`SELECT url, snippet(corpus_fts,1,'[',']',' ... ',16) FROM corpus_fts WHERE corpus_fts MATCH ? LIMIT 20`, strings.Join(terms, " AND "))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer rows.Close()
		results := []map[string]string{}
		for rows.Next() {
			var url, snippet string
			if rows.Scan(&url, &snippet) == nil {
				results = append(results, map[string]string{"url": url, "snippet": snippet})
			}
		}
		writeJSON(w, map[string]interface{}{"results": results})
	})
	mux.HandleFunc("/api/keywords", func(w http.ResponseWriter, r *http.Request) {
		docs := corpusDocuments(500)
		counts := make(map[string]int)
		for _, d := range docs {
			for _, tok := range tokenize(d["text"]) {
				lower := strings.ToLower(tok)
				if len(tok) > 3 && !guiStopwords[lower] && corpusfreqlib.Freq(tok) < 100 {
					counts[strings.ToLower(tok)]++
				}
			}
		}
		type item struct {
			Word  string
			Count int
		}
		items := []item{}
		for word, count := range counts {
			items = append(items, item{word, count})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Count > items[j].Count })
		words := []string{}
		for i, it := range items {
			if i >= 40 {
				break
			}
			words = append(words, it.Word)
		}
		writeJSON(w, map[string]interface{}{"keywords": words})
	})
	mux.HandleFunc("/api/keywords/apply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Profile  string   `json:"profile"`
			Keywords []string `json:"keywords"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		name := filepath.Base(body.Profile)
		path := "profiles/" + name + ".yaml"
		b, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		text := string(b)
		for _, key := range []string{"linkOk:", "regexRankingKeywords:"} {
			if i := strings.Index(text, key); i >= 0 {
				headerEnd := strings.Index(text[i:], "\n")
				if headerEnd < 0 {
					continue
				}
				contentStart := i + headerEnd + 1
				contentEnd := strings.Index(text[contentStart:], "\n")
				if contentEnd < 0 {
					contentEnd = len(text) - contentStart
				}
				text = text[:contentStart] + "  " + strings.Join(body.Keywords, "|") + text[contentStart+contentEnd:]
			}
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "applied"})
	})
	mux.HandleFunc("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		var body struct {
			Profile string `json:"profile"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		name := filepath.Base(body.Profile)
		if name == "" || name == "." {
			http.Error(w, "profile required", 400)
			return
		}
		if err := launchGUICrawl(name); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, map[string]string{"status": "started", "profile": name})
	})
	mux.HandleFunc("/api/crawl/stop", func(w http.ResponseWriter, r *http.Request) {
		app.StopRequested.Store(true)
		writeJSON(w, map[string]string{"status": "stopping"})
	})

	return mux
}

// runGUI starts the embedded local web interface on loopback only.
func runGUI() error {
	if app.GUIPort <= 0 || app.GUIPort > 65535 {
		app.GUIPort = 8765
	}
	base := app.GUIPort
	var listener net.Listener
	var addr string
	var err error
	for port := base; port < base+20; port++ {
		addr = fmt.Sprintf("127.0.0.1:%d", port)
		listener, err = net.Listen("tcp", addr)
		if err == nil {
			app.GUIPort = port
			break
		}
	}
	if listener == nil {
		return fmt.Errorf("no free port between %d and %d: %w", base, base+19, err)
	}
	fmt.Printf("goCrawler Workbench: http://%s\n", addr)
	return http.Serve(listener, guiHandler())
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

// corpusDocuments reads accepted documents from the curated corpus.
func corpusDocuments(limit int) []map[string]string {
	result := []map[string]string{}
	path := "./logs/corpusCuratedText.log"
	if !iolib.FileExists(path) {
		return result
	}
	var url, text string
	flush := func() {
		if url != "" && len(result) < limit {
			result = append(result, map[string]string{"url": url, "text": strings.TrimSpace(text)})
		}
	}
	for _, line := range strings.Split(iolib.File2string(path), "\n") {
		if strings.HasPrefix(line, "http") {
			flush()
			url, text = strings.TrimSpace(line), ""
		} else if line != "----" {
			text += line + "\n"
		}
	}
	flush()
	return result
}

// usage prints the command-line usage message.
func usage() {
	fmt.Println("Usage: go run crawler.go <profile>")
	fmt.Println("  <profile>  profile name (without .yaml), from profiles/ or the working directory")
	fmt.Println("  --selftest run built-in checks and exit")
	fmt.Println("  --resume   restore a previous crawl from the cache store")
	fmt.Println("  -profile=, -backend=, -cache=, -log-level=, -workers=N, -max-links=N")
	fmt.Println("  -dry-run, -explore, -query=TEXT, -generate-profile=URL, -version, -license, -gui [-gui-port=N]")
	fmt.Println("Examples: go run crawler.go crypto --resume | go run crawler.go -query=drizzle")
}

// startSessionOutput tees stdout to the terminal and to a timestamped file in logs/.
// It returns a function that restores stdout and closes the file.
func startSessionOutput(name string) func() {
	now := time.Now().UTC()
	if name == "" {
		name = app.ProfileName
	}
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	filename := fmt.Sprintf("./logs/%s-%s-Z%02d-%02d-%02d.txt",
		name, now.Format("2006-01-02"), now.Hour(), now.Minute(), now.Second())

	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Cannot create session log %s: %v\n", filename, err)
		return func() {}
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		fmt.Printf("Cannot tee stdout: %v\n", err)
		file.Close()
		return func() {}
	}

	original := os.Stdout
	os.Stdout = writer
	done := make(chan struct{})
	go func() {
		io.Copy(io.MultiWriter(original, file), reader)
		close(done)
	}()

	fmt.Printf("Session output: %s\n", filename)

	return func() {
		os.Stdout = original
		writer.Close()
		<-done
		file.Close()
	}
}

// main initializes the crawler and drives the processing loop.
// prepareCLI handles informational modes before any runtime setup.
func prepareCLI() (bool, error) {
	if len(os.Args) >= 2 && os.Args[1] == "--selftest" {
		if runSelfTests() {
			return true, nil
		}
		return true, fmt.Errorf("self-test failed")
	}
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		return true, nil
	}
	for _, arg := range os.Args[1:] {
		if arg == "--resume" || arg == "-resume" {
			app.ResumeEnabled = true
		}
	}
	if err := parseCLI(); err != nil {
		if err == errStop {
			return true, nil
		}
		return true, err
	}
	if app.CLIGenerate != "" {
		return true, generateProfile(app.CLIGenerate, "./profiles/generated.yaml")
	}
	if app.CLIQuery != "" {
		return true, runSearch(app.CLIQuery)
	}
	if app.CLIExplore {
		return true, exploreCorpus()
	}
	if appGUI {
		return true, runGUI()
	}
	return false, nil
}

// validateConfig checks the resolved configuration before a crawl starts.
func validateConfig() error {
	var problems []string
	if app.MinDocLen < 0 || app.MaxDocLen <= app.MinDocLen {
		problems = append(problems, fmt.Sprintf("invalid document length range %d..%d", app.MinDocLen, app.MaxDocLen))
	}
	if app.ScoreThreshold < 0 {
		problems = append(problems, "scoreThreshold must be >= 0")
	}
	if app.MaxLinksPerPage <= 0 {
		problems = append(problems, "maxLinksPerPage must be > 0")
	}
	if app.CLIWorkers < 1 || app.CLIWorkers > 32 {
		problems = append(problems, "workers must be between 1 and 32")
	}
	switch app.RankingStrategy {
	case "keywords", "tfidf", "semantic", "hybrid":
	default:
		problems = append(problems, "unknown rankingStrategy: "+app.RankingStrategy)
	}
	switch app.CacheBackend {
	case storelib.BackendLocal, storelib.BackendRedis:
	default:
		problems = append(problems, "unknown cacheBackend: "+app.CacheBackend)
	}
	for name, expression := range map[string]string{
		"regexBannedDomains":    app.RegexBannedDomains,
		"regexLinkBannedTokens": app.RegexLinkBannedTokens,
		"regexStopwords":        app.RegexStopwords,
		"regexLinkOk":           app.RegexLinkOk,
		"regexRankingKeywords":  app.RegexRankingKeywords,
	} {
		if _, err := regexp.Compile(expression); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

// setupRuntime prepares folders, configuration, stores and outputs.
func setupRuntime(profile string) (int, func(), error) {
	noop := func() {}
	if err := os.MkdirAll("./logs", os.ModePerm); err != nil {
		return 0, noop, err
	}
	if err := os.MkdirAll("./output", os.ModePerm); err != nil {
		return 0, noop, err
	}

	stopSession := startSessionOutput(profile)
	cleanup := func() {
		stopSession()
		if app.CSVFile != nil {
			app.CSVFile.Close()
		}
		if app.SearchDB != nil {
			app.SearchDB.Close()
		}
		if app.CacheStore != nil {
			app.CacheStore.Close()
		}
		saveCrawlState()
		closeFileLoggers()
	}

	if err := initFileLoggers(); err != nil {
		stopSession()
		return 0, noop, err
	}
	initRejectedLinkSets()

	if err := yamlInitGeneral(); err != nil {
		return 0, cleanup, err
	}
	initLogger(app.LogLevel)
	if err := yamlInitProxy(); err != nil {
		return 0, cleanup, err
	}
	initHTTPClient()
	if err := yamlInitSpecific(); err != nil {
		return 0, cleanup, err
	}
	compileConfigRegexes()
	if err := validateConfig(); err != nil {
		return 0, cleanup, err
	}

	var err error
	app.CacheStore, err = storelib.Init(app.CacheBackend, app.CachePath)
	if err != nil {
		return 0, cleanup, fmt.Errorf("cache store error: %w", err)
	}
	if err := initSearchIndex(); err != nil {
		return 0, cleanup, fmt.Errorf("search index: %w", err)
	}

	startIndex := 0
	if app.ResumeEnabled {
		startIndex = loadCrawlState()
	} else if err := app.CacheStore.Set(crawlStateKey, ""); err != nil {
		app.Logger.Warn("cannot clear previous crawl state", "error", err)
	}

	if app.RespectRobots {
		app.RobotsPolicy = robotspolicy.New(app.UserAgent, app.DownloadTimeout)
	}
	if err := corpusfreqlib.Init(); err != nil {
		return 0, cleanup, fmt.Errorf("English corpus: %w", err)
	}

	if !app.ResumeEnabled {
		linkBootstraping()
	} else {
		fmt.Printf("* Resuming with %d links in the pool ...\n", len(app.LPool))
	}
	if err := csvInit(); err != nil {
		return 0, cleanup, err
	}
	if !app.ResumeEnabled {
		iolib.String2file("\n", "./logs/corpusCuratedText.log")
	}
	return startIndex, cleanup, nil
}

// crawlLoop runs the worker pool and returns the final processed index.
func crawlLoop(startIndex int) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupt received, saving state...")
		app.StopRequested.Store(true)
		app.StateDirty = true
	}()

	var nextIndex atomic.Int64
	nextIndex.Store(int64(startIndex))
	var wg sync.WaitGroup
	for i := 0; i < app.CLIWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if app.StopRequested.Load() {
					return
				}
				index := int(nextIndex.Add(1) - 1)
				app.CrawlMu.Lock()
				app.LastProcessedLink = index
				app.CrawlMu.Unlock()
				if !doNextLink(index) {
					return
				}
			}
		}()
	}
	wg.Wait()
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	return app.LastProcessedLink
}

// reportResults writes the final summaries.
func reportResults(finalIndex int) {
	dumpFrequencies(finalIndex, true)
	app.Logger.Info("crawl finished", "processed", finalIndex,
		"accepted", app.AcceptedDocs, "rejections", rejectionSummary())
	fmt.Println("\n\n\n***** Done!!!")
}

// run initializes the crawler, executes the crawl and reports results.
func run() error {
	app = newCrawler()
	handled, err := prepareCLI()
	if err != nil {
		return err
	}
	if handled {
		return nil
	}

	startIndex, cleanup, err := setupRuntime(app.ProfileName)
	if err != nil {
		return err
	}
	defer cleanup()

	if app.CLIDryRun {
		fmt.Printf("Dry run: profile=%s backend=%s cache=%s language=%s strategy=%s workers=%d\n",
			app.ProfileName, app.CacheBackend, app.CachePath, app.TargetLanguage, app.RankingStrategy, app.CLIWorkers)
		return nil
	}

	finalIndex := crawlLoop(startIndex)
	reportResults(finalIndex)
	return nil
}

// main runs the crawler and terminates with a non-zero status on error.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// setupTestConfig sets the global regular expressions used by the pure filters.
// It is shared by --selftest and the unit tests.
func setupTestConfig() {
	app.EngStopWordsWOthe = "a|and|of|the|with"
	app.EngStopWords = "the|" + app.EngStopWordsWOthe
	app.EngLowRelevancyWords = "|news|info"
	app.RegexStopwords = `(?i)\W([0-9]+|.|..|` + app.EngStopWordsWOthe + app.EngLowRelevancyWords + `)\W`
	app.RegexRankingKeywords = `(?i)\W(stack|stacking|image|noise|signal|siril)\W`
	app.RegexLinkOk = `(?i)^https*://.*(stack|siril|astro|image)`
	app.RegexBannedDomains = `(?i)(facebook|twitter)\.(com|org)`
	app.RegexLinkBannedTokens = `(?i)(login|signup|pdf|\.(pdf|jpg))`
	compileConfigRegexes()
	app.RankingStrategy = "hybrid"
	app.Synonyms = make(map[string][]string)
	buildRankingRegex()
	app.CorpusDocFreq = make(map[string]int)
	app.CorpusDocCount = 0
}

// check reports the result of one self-test and accumulates the global outcome.
func check(name string, ok bool, allOK *bool) {
	status := "FAIL"
	if ok {
		status = "PASS"
	} else {
		*allOK = false
	}
	fmt.Printf("[%s] %s\n", status, name)
}

// runSelfTests executes built-in sanity checks without network access.
func runSelfTests() bool {
	allOK := true
	fmt.Println("goCrawler self-test:")

	check("tokenizer", reflect.DeepEqual(
		tokenize("a, b! c"), []string{"a", "b", "c"}), &allOK)
	check("domain extraction", getDomain("https://www.siril.org/docs/") == "siril.org", &allOK)
	check("second-level domain", getSecondLevelDomain("https://www.siril.org/docs/") == "siril.org", &allOK)

	setupTestConfig()
	check("keyword ranking", rankingByKeywords("stack image noise signal siril") > 0 &&
		rankingByKeywords("nothing relevant here") == 0, &allOK)
	check("n-gram extraction", len(ngramsOf("stack image noise", 2)) > 0, &allOK)

	if err := corpusfreqlib.Init(); err != nil {
		check("English corpus", false, &allOK)
	} else {
		check("English corpus", corpusfreqlib.Freq("the") > 0, &allOK)
	}
	check("robots policy", robotspolicy.New("goCrawler-selftest", 0) != nil, &allOK)

	freqTest := make(freq)
	freqTest.add("alpha")
	freqTest.add("alpha")
	freqTest.add("beta")
	check("frequency counting", freqTest["alpha"] == 2 && rSortFreq(freqTest)[0].Key == "alpha", &allOK)

	old, err := os.Getwd()
	if err != nil {
		fmt.Println("[FAIL] working directory:", err)
		return false
	}
	dir, err := os.MkdirTemp("", "gocrawler-selftest-")
	if err != nil {
		fmt.Println("[FAIL] temp directory:", err)
		return false
	}
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
		fmt.Println("[FAIL] logs directory:", err)
		return false
	}
	for name := range app.RejectedLinkSets {
		app.RejectedLinkSets[name] = make(map[string]struct{})
	}
	if err := os.Chdir(dir); err != nil {
		fmt.Println("[FAIL] chdir:", err)
		return false
	}
	defer os.Chdir(old)

	check("link validation", linkSeemsOk("https://siril.org/tutorials/stacking", false) &&
		!linkSeemsOk("javascript:void(0)", false), &allOK)

	store, err := storelib.Init(storelib.BackendLocal, filepath.Join(dir, "cache", "cache.db"))
	if err != nil {
		check("local cache store", false, &allOK)
	} else {
		ok := store.Set("selftest", "value") == nil && store.Exists("selftest") &&
			store.Get("selftest") == "value"
		if closeErr := store.Close(); closeErr != nil {
			ok = false
		}
		check("local cache store", ok, &allOK)
	}

	check("unknown cache backend", func() bool {
		_, err := storelib.Init("unknown", "")
		return err != nil
	}(), &allOK)

	check("rejected-link dedup", func() bool {
		logRejectedLink("linksNotOk.log", "https://example.org/bad")
		logRejectedLink("linksNotOk.log", "https://example.org/bad")
		b, err := os.ReadFile("logs/linksNotOk.log")
		return err == nil && strings.Count(string(b), "https://example.org/bad") == 1
	}(), &allOK)

	if allOK {
		fmt.Println("Self-test: ALL PASSED")
	} else {
		fmt.Println("Self-test: FAILED")
	}
	return allOK
}
