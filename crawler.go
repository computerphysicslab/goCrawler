// Package main implements a topic-restricted web crawler that builds curated English corpora.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"container/heap"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
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
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"

	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/skip2/go-qrcode"

	"log/slog"

	"github.com/jackdanger/collectlinks"
	snowballeng "github.com/kljensen/snowball/english"
	"github.com/spf13/viper"
	"jaytaylor.com/html2text"

	"github.com/abadojack/whatlanggo"

	"github.com/computerphysicslab/goPackages/goDebug"

	"goCrawler/corpusfreqlib"
	"goCrawler/iolib"
	"goCrawler/pdftextlib"
	"goCrawler/robotspolicy"
	"goCrawler/storelib"
	"goCrawler/stringlib"
)

// Crawler owns the configuration, runtime state and stores of one crawl.
// Keeping them together removes package-level mutable globals and makes the
// crawler embeddable and independently testable.
type queueEvent struct {
	URL   string    `json:"url"`
	State string    `json:"state"`
	Time  time.Time `json:"time"`
}

type rejectionEvent struct {
	URL     string    `json:"url"`
	Reason  string    `json:"reason,omitempty"`
	Profile string    `json:"profile,omitempty"`
	Seed    string    `json:"seed,omitempty"`
	Detail  string    `json:"detail"`
	Text    string    `json:"text,omitempty"`
	Time    time.Time `json:"time"`
}

type corpusExportInfo struct {
	File       string `json:"file"`
	Download   string `json:"download"`
	Documents  int    `json:"documents"`
	Characters int    `json:"characters"`
	Profile    string `json:"profile"`
	Scope      string `json:"scope"`
}

type Crawler struct {
	// configuration
	RegexBannedDomains       string
	RegexLinkBannedTokens    string
	CuratedDomains           string
	RegexLinkOk              string
	EngStopWordsWOthe        string
	EngStopWords             string
	EngLowRelevancyWords     string
	RegexStopwords           string
	RegexRankingKeywords     string
	ReRankingExtended        *regexp.Regexp
	ProxyHost                string
	ProxyUser                string
	ProxyPass                string
	DownloadTimeout          time.Duration
	MaxDownloadBytes         int64
	ProcessPDF               bool
	PDFMaxBytes              int64
	PDFPagesLimit            int
	BootstrapLinks           []string
	MinDocLen                int
	MaxDocLen                int
	MinStopwordRatio         float64
	MaxStopwordRatio         float64
	MaxLinksPerPage          int
	CrawlDelay               time.Duration
	RankingStrategy          string
	Synonyms                 map[string][]string
	ScoreThreshold           float64
	AddURL                   string
	CacheBackend             string
	CachePath                string
	UserAgent                string
	RespectRobots            bool
	LogLevel                 string
	ProfileName              string
	NotifyWebhookURL         string
	NotifyDesktop            bool
	NotifyOnCompletion       bool
	NotifyOnError            bool
	RejectionNotifyThreshold int
	TargetLanguage           string
	MinLanguageConfidence    float64
	ConfigFile               string
	CLIWorkers               int
	CLIDryRun                bool
	CLIQuery                 string
	CLIExplore               bool
	CLIGenerate              string
	CLIProfileLint           string
	CLIRejectReport          string
	CLITraceURL              string
	CLITraceFile             string
	RejectTracePath          string
	LogRotateDays            int
	LogRotateMaxMB           int
	rejectTraceFile          *os.File
	rejectTraceMu            sync.Mutex
	minRatioFromCLI          bool
	maxRatioFromCLI          bool
	ShowVersion              bool
	ShowLicense              bool
	GUIPort                  int
	GUILastError             string
	ResumeEnabled            bool

	// state
	LPool             []aLink
	LinkIndex         map[string]int
	VisitedURLs       map[string]bool
	DomainCounter     map[string]int
	FailedDomains     map[string]int
	UniqueSignature   map[string]string
	CorpusFreqs       freq
	CorpusDocFreq     map[string]int
	CorpusDocCount    int
	RejectionCounts   map[string]int
	RejectionEvents   map[string][]rejectionEvent
	notifiedReasons   map[string]bool
	RejectionMu       sync.Mutex
	QueueEvents       []queueEvent
	QueueMu           sync.Mutex
	AcceptedDocs      int
	LastProcessedLink int
	StateDirty        bool
	CacheHits         int
	OnlineDownloads   int
	DownloadMS        int64
	CrawlStartedAt    time.Time
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
		PDFMaxBytes:      25 << 20,
		PDFPagesLimit:    pdftextlib.DefaultPageLimit,
		MinStopwordRatio: 0.1,
		MaxStopwordRatio: 0.38,
		RejectTracePath:  "./logs/rejections.jsonl",
		LogRotateDays:    14,
		LogRotateMaxMB:   500,
		RankingStrategy:  "hybrid",
		Synonyms:         make(map[string][]string),
		LinkIndex:        make(map[string]int),
		VisitedURLs:      make(map[string]bool),
		DomainCounter:    make(map[string]int),
		FailedDomains:    make(map[string]int),
		UniqueSignature:  make(map[string]string),
		CorpusFreqs:      make(freq),
		CorpusDocFreq:    make(map[string]int),
		RejectionCounts:  make(map[string]int),
		RejectionEvents:  make(map[string][]rejectionEvent),
		notifiedReasons:  make(map[string]bool),
		QueueEvents:      []queueEvent{},
		RejectedLinkSets: map[string]map[string]struct{}{
			"linksNotOk.log":                 {},
			"linksNotOk.additional.log":      {},
			"linksNotOk.ParseRequestURI.log": {},
			"robotsBlocked.log":              {},
		},
		DomainLastReq:      make(map[string]time.Time),
		HTTPClient:         &http.Client{Timeout: 10 * time.Second},
		Logger:             slog.New(slog.NewTextHandler(os.Stdout, nil)),
		LogDownload:        log.New(io.Discard, "", 0),
		LogCGI:             log.New(io.Discard, "", 0),
		LogDomainFailed:    log.New(io.Discard, "", 0),
		CLIWorkers:         4,
		GUIPort:            8765,
		TargetLanguage:     "English",
		CachePath:          "./cache/cache.db",
		LogLevel:           "info",
		NotifyOnCompletion: true,
		NotifyOnError:      true,
	}
}

// ResetForProfile clears crawl state while keeping GUI settings stable.
func (c *Crawler) ResetForProfile(profile string) {
	c.ProfileName = profile
	c.LPool = nil
	c.LinkIndex = make(map[string]int)
	c.VisitedURLs = make(map[string]bool)
	c.DomainCounter = make(map[string]int)
	c.FailedDomains = make(map[string]int)
	c.UniqueSignature = make(map[string]string)
	c.CorpusFreqs = make(freq)
	c.CorpusDocFreq = make(map[string]int)
	c.CorpusDocCount = 0
	c.RejectionCounts = make(map[string]int)
	c.RejectionEvents = make(map[string][]rejectionEvent)
	c.notifiedReasons = make(map[string]bool)
	c.AcceptedDocs = 0
	c.LastProcessedLink = 0
	c.StateDirty = false
	c.CacheHits = 0
	c.OnlineDownloads = 0
	networkRequests.Store(0)
	robotspolicy.ResetStats()
	storelib.ResetStats()
	c.DownloadMS = 0
	c.CrawlStartedAt = time.Time{}
	c.GUILastError = ""
	c.StopRequested.Store(false)
	for name := range c.RejectedLinkSets {
		c.RejectedLinkSets[name] = make(map[string]struct{})
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
const crawlStateVersion = 2

type crawlState struct {
	Version       int               `json:"version"`
	Checksum      string            `json:"checksum,omitempty"`
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

// crawlStateChecksum hashes the canonical JSON of a state with its checksum
// field blanked, so the digest can be stored alongside the data.
func crawlStateChecksum(state crawlState) string {
	state.Checksum = ""
	b, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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
	state.Checksum = crawlStateChecksum(state)
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
	switch state.Version {
	case 1:
		// Legacy snapshots predate checksums; accept them with a warning.
		app.Logger.Warn("legacy crawl state without checksum accepted", "version", state.Version)
	case 2:
		if state.Checksum == "" || state.Checksum != crawlStateChecksum(state) {
			app.Logger.Error("crawl state checksum mismatch, refusing to resume")
			return 0
		}
	default:
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

// recordQueueEvent keeps the latest link-state transitions for the live view.
func recordQueueEvent(state, url string) {
	app.QueueMu.Lock()
	defer app.QueueMu.Unlock()
	app.QueueEvents = append(app.QueueEvents, queueEvent{URL: url, State: state, Time: time.Now()})
	if len(app.QueueEvents) > 30 {
		app.QueueEvents = app.QueueEvents[len(app.QueueEvents)-30:]
	}
}

// countRejection increments the app.Counter for one rejection reason.
func countRejection(reason string) {
	recordRejection(reason, "", "", "")
}

// seedForRejectedURL returns the bootstrap seed when the rejected URL is one
// of them. Full seed provenance is not tracked in aLink, so other links return
// an empty seed (reported honestly by the trace).
func seedForRejectedURL(link string) string {
	trim := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
	for _, seed := range app.BootstrapLinks {
		if trim(seed) == trim(link) {
			return seed
		}
	}
	return ""
}

// recordRejection increments a counter, keeps the latest cases for the GUI and
// streams every event to the JSONL trace used by the report and trace modes.
func recordRejection(reason, url, detail, text string) {
	app.CrawlMu.Lock()
	app.RejectionCounts[reason]++
	count := app.RejectionCounts[reason]
	threshold := app.RejectionNotifyThreshold
	shouldNotify := threshold > 0 && count >= threshold && !app.notifiedReasons[reason]
	if shouldNotify {
		app.notifiedReasons[reason] = true
	}
	app.CrawlMu.Unlock()

	if shouldNotify {
		notify("rejection_threshold", fmt.Sprintf("rejection reason %q reached %d", reason, count),
			map[string]any{"reason": reason, "count": count})
	}

	app.RejectionMu.Lock()
	defer app.RejectionMu.Unlock()
	event := rejectionEvent{
		URL: url, Reason: reason, Profile: app.ProfileName, Seed: seedForRejectedURL(url),
		Detail: detail, Text: text, Time: time.Now(),
	}
	events := app.RejectionEvents[reason]
	events = append(events, event)
	if len(events) > 10 {
		events = events[len(events)-10:]
	}
	app.RejectionEvents[reason] = events
	if app.rejectTraceFile != nil {
		if line, err := json.Marshal(event); err == nil {
			app.rejectTraceMu.Lock()
			_, _ = app.rejectTraceFile.Write(append(line, '\n'))
			app.rejectTraceMu.Unlock()
		}
	}
}

const notificationTimeout = 3 * time.Second

// crawlSummary returns a consistent snapshot of the final counters.
func crawlSummary() (int, map[string]int) {
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	counts := make(map[string]int, len(app.RejectionCounts))
	for k, v := range app.RejectionCounts {
		counts[k] = v
	}
	return app.AcceptedDocs, counts
}

// notify dispatches an optional webhook and desktop notification.
func notify(event, message string, extra map[string]any) {
	if app.NotifyWebhookURL == "" && !app.NotifyDesktop {
		return
	}
	payload := map[string]any{
		"event":   event,
		"message": message,
		"profile": app.ProfileName,
		"time":    time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range extra {
		payload[k] = v
	}

	if app.NotifyWebhookURL != "" {
		body, err := json.Marshal(payload)
		if err != nil {
			app.Logger.Error("cannot encode notification", "error", err)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, app.NotifyWebhookURL, bytes.NewReader(body))
			if reqErr != nil {
				cancel()
				app.Logger.Error("cannot create notification request", "error", reqErr)
			} else {
				req.Header.Set("Content-Type", "application/json")
				client := &http.Client{Timeout: notificationTimeout}
				resp, doErr := client.Do(req)
				if doErr != nil {
					app.Logger.Warn("notification webhook failed", "event", event, "error", doErr)
				} else {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode >= 300 {
						app.Logger.Warn("notification webhook status", "event", event, "status", resp.StatusCode)
					}
				}
				cancel()
			}
		}
	}

	if app.NotifyDesktop {
		go func() {
			if err := sendDesktopNotification(event + ": " + message); err != nil {
				app.Logger.Debug("desktop notification unavailable", "error", err)
			}
		}()
	}
}

// sanitizeNotifyText keeps desktop notification strings on one line.
func sanitizeNotifyText(s string) string {
	replacer := strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\r", " ", "\n", " ")
	return replacer.Replace(s)
}

// sendDesktopNotification uses the platform's local notifier, never a shell.
func sendDesktopNotification(message string) error {
	title := "goCrawler"
	clean := sanitizeNotifyText(message)
	var name string
	var args []string
	switch runtime.GOOS {
	case "linux":
		name, args = "notify-send", []string{title, clean}
	case "darwin":
		name, args = "osascript", []string{"-e", `display notification "` + clean + `" with title "` + title + `"`}
	case "windows":
		name = "powershell"
		args = []string{"-NoProfile", "-Command",
			`Add-Type -AssemblyName System.Windows.Forms;$n=New-Object System.Windows.Forms.NotifyIcon;$n.Icon=[System.Drawing.SystemIcons]::Information;$n.Visible=$true;$n.ShowBalloonTip(3000,"` + title + `","` + clean + `",[System.Windows.Forms.ToolTipIcon]::Info)`}
	default:
		return fmt.Errorf("desktop notifications unsupported on %s", runtime.GOOS)
	}
	ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run()
}

// notifyCompletion sends the end-of-crawl notification when enabled.
func notifyCompletion(processed int) {
	if !app.NotifyOnCompletion {
		return
	}
	accepted, counts := crawlSummary()
	notify("crawl_completed", fmt.Sprintf("crawl finished: %d processed, %d accepted", processed, accepted),
		map[string]any{"processed": processed, "accepted": accepted, "rejections": counts})
}

// notifyError sends an error notification when enabled.
func notifyError(message string) {
	if !app.NotifyOnError {
		return
	}
	notify("crawl_error", message, map[string]any{"profile": app.ProfileName})
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
	URL        string
	Domain     string
	Count      int
	Status     int       // 0 = pending, 1 = crawling, 2 = downloaded, 3 = failed, 4 = bootstrapping
	ReservedAt time.Time // when a worker picked it up
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
	var req *http.Request
	if ctx := guiCrawlContext(); ctx != nil {
		req, err = http.NewRequestWithContext(ctx, "GET", urlLink, nil)
	} else {
		req, err = http.NewRequest("GET", urlLink, nil)
	}
	if err != nil {
		return
	}
	if printLog {
		fmt.Printf("\nStart downloading")
	}
	networkRequests.Add(1)
	resp, err = app.HTTPClient.Do(req)
	if printLog {
		fmt.Printf("\nEnd downloading")
	}
	return
}

// fetchedPage holds a downloaded response body and its declared content type.
type fetchedPage struct {
	Body        []byte
	ContentType string
}

// errPDFDetected signals that the response is a PDF document.
var errPDFDetected = errors.New("download: pdf document")

// fetchPage downloads the body of urlLink up to the applicable size limit.
func fetchPage(urlLink string) (*fetchedPage, error) {
	app.LogDownload.Printf("\n%s", urlLink)
	domain := getDomain(urlLink)
	if domainHadFailed(domain) {
		app.LogDownload.Printf("\tPreviously failed")
		return nil, errors.New("download: Previously failed")
	}
	app.LogDownload.Printf("\tRequested")

	resp, err := proxyGet(urlLink, true)
	if err != nil {
		app.LogDownload.Printf("\tHttp transport error:\n\t\t%s", err)
		domainReportFailed(domain)
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		app.LogDownload.Printf("\tHTTP status: %d", resp.StatusCode)
		if resp.StatusCode >= 500 {
			domainReportFailed(domain)
		}
		resp.Body.Close()
		return nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	limit := app.MaxDownloadBytes
	if app.ProcessPDF && app.PDFMaxBytes > limit &&
		(isPDFURL(urlLink) || strings.Contains(strings.ToLower(contentType), "application/pdf")) {
		limit = app.PDFMaxBytes
	}

	bodyBytes, err := ioutil.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		app.LogDownload.Printf("\t%s%s", "Read error: ", err)
		resp.Body.Close()
		return nil, err
	}
	resp.Body.Close() // must close
	app.LogDownload.Printf("\tFetched %d bytes (%s)", len(bodyBytes), contentType)

	return &fetchedPage{Body: bodyBytes, ContentType: contentType}, nil
}

// isPDFURL reports whether the URL path ends in .pdf.
func isPDFURL(link string) bool {
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	return strings.EqualFold(path.Ext(u.Path), ".pdf")
}

// isPDFPage detects PDF documents by Content-Type or magic bytes.
func isPDFPage(page *fetchedPage) bool {
	if strings.Contains(strings.ToLower(page.ContentType), "application/pdf") {
		return true
	}
	return bytes.HasPrefix(page.Body, []byte("%PDF-"))
}

// htmlFromPage converts an HTML response body into plain text and links.
func htmlFromPage(page *fetchedPage) (string, []string, error) {
	if ct := page.ContentType; ct != "" &&
		!strings.Contains(ct, "html") && !strings.Contains(ct, "text") && !strings.Contains(ct, "xml") {
		return "", nil, fmt.Errorf("download: unsupported content type %s", ct)
	}

	links := collectlinks.All(bytes.NewReader(page.Body))
	plain, err := html2text.FromString(string(page.Body), html2text.Options{PrettyTables: false})
	if err != nil {
		return "", nil, err
	}
	return plain, links, nil
}

// download fetches a page and returns its plain text and extracted links.
func download(urlLink string) (string, []string, error) {
	page, err := fetchPage(urlLink)
	if err != nil {
		return "", nil, err
	}
	if isPDFPage(page) {
		return "", nil, errPDFDetected
	}
	plain, links, err := htmlFromPage(page)
	if err != nil {
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
			recordRejection("robots_blocked", urlLink, "disallowed by robots.txt", "")
			logRejectedLink("robotsBlocked.log", urlLink)
			fmt.Printf(" [ROBOTS-BLOCKED]")
			return "", nil, errors.New("download: blocked by robots.txt")
		}
		app.RobotsPolicy.Wait(urlLink)
	}
	respectCrawlDelay(urlLink)

	start := time.Now()
	page, err := fetchPage(urlLink)
	app.DownloadMS += time.Since(start).Milliseconds()
	if err != nil {
		return "", nil, err
	}
	if isPDFPage(page) {
		if !app.ProcessPDF {
			recordRejection("pdf_disabled", urlLink, "PDF found but process_pdf is false", "")
			return "", nil, errors.New("download: PDF found but process_pdf is disabled in this profile")
		}
		app.OnlineDownloads++
		return downloadPDFCached(urlLink, page)
	}

	content, links, err = htmlFromPage(page)
	if err != nil {
		return "", nil, err
	}
	app.OnlineDownloads++
	cached.Content, cached.Links = content, links
	encoded, marshalErr := json.Marshal(cached)
	if marshalErr != nil {
		app.Logger.Error("cannot marshal cache entry", "error", marshalErr)
	} else if setErr := app.CacheStore.Set(cacheKey, string(encoded)); setErr != nil {
		app.Logger.Error("cannot write cache entry", "error", setErr)
	}
	fmt.Printf(" [ONLINE]")
	return content, links, nil
}

// downloadPDFCached extracts text from a PDF page and caches the result.
func downloadPDFCached(urlLink string, page *fetchedPage) (string, []string, error) {
	cacheKey := "pdf_" + urlLink
	var cached CachedData
	if app.CacheStore.Exists(cacheKey) {
		value := app.CacheStore.Get(cacheKey)
		if value != "" {
			if err := json.Unmarshal([]byte(value), &cached); err != nil {
				app.Logger.Warn("discarding corrupt pdf cache entry", "key", cacheKey, "error", err)
			} else {
				app.CacheHits++
				fmt.Printf(" [CACHE]")
				return cached.Content, nil, nil
			}
		}
	}

	if int64(len(page.Body)) > app.PDFMaxBytes {
		recordRejection("too_large", urlLink, fmt.Sprintf("%d bytes > max %d", len(page.Body), app.PDFMaxBytes), "")
		return "", nil, fmt.Errorf("download: pdf too large (%d bytes)", len(page.Body))
	}

	text, pages, err := pdftextlib.Extract(page.Body, app.PDFPagesLimit)
	if err != nil {
		recordRejection("invalid_pdf", urlLink, err.Error(), "")
		app.Logger.Debug("pdf extraction failed", "url", urlLink, "error", err)
		return "", nil, err
	}
	if strings.TrimSpace(text) == "" {
		recordRejection("pdf_no_text", urlLink, "no extractable text layer", "")
		return "", nil, errors.New("download: pdf has no text layer")
	}
	app.Logger.Debug("pdf extracted", "url", urlLink, "pages", pages, "chars", len(text))

	encoded, marshalErr := json.Marshal(CachedData{Content: text})
	if marshalErr != nil {
		app.Logger.Error("cannot marshal pdf cache entry", "error", marshalErr)
	} else if setErr := app.CacheStore.Set(cacheKey, string(encoded)); setErr != nil {
		app.Logger.Error("cannot write pdf cache entry", "error", setErr)
	}
	return text, nil, nil
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

	// PDF URLs stay banned unless the profile explicitly opts in.
	if app.ProcessPDF && isPDFURL(link) {
		return false
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
		recordRejection("invalid_url", l, "url.ParseRequestURI failed", "")
		logRejectedLink("linksNotOk.ParseRequestURI.log", l)
		return false
	}

	// Additional URL validator to avoid panic error cases like this:
	// http://(http://health.org/2010/04/17/
	matches := reURLStrict.FindAllStringSubmatch(l, -1)
	if len(matches) < 1 {
		recordRejection("invalid_url", l, "strict URL regex failed", "")
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
			recordRejection("off_topic_url", l, "URL does not match linkOk/curatedDomains", "")
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
		if l.Status == 1 && !l.ReservedAt.IsZero() && time.Since(l.ReservedAt) > linkStaleTimeout {
			app.LPool[i].Status = 0
			app.LPool[i].ReservedAt = time.Time{}
			l = app.LPool[i]
		}
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
	app.LPool[maxi].ReservedAt = time.Now()
	app.VisitedURLs[maxURL] = true
	app.StateDirty = true
	recordQueueEvent("requested", maxURL)
	app.DomainCounter[app.LPool[maxi].Domain]++
	app.StateDirty = true

	return maxi, maxURL
}

// addLink validates a link and adds or updates it in the pool.
func addLink(link string, avoidFilters bool) bool {
	app.PoolMu.Lock()
	defer app.PoolMu.Unlock()
	return addLinkLocked(link, avoidFilters)
}

// addLinkLocked assumes PoolMu is held.
func addLinkLocked(link string, avoidFilters bool) bool {
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

	// Never process the same URL twice in one run, even after pruning.
	if app.VisitedURLs[link] {
		return false
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
		term := ""
		if len(k) > 1 {
			term = k[1]
		} else if len(k) > 0 {
			term = k[0]
		}
		if len(term) < 3 {
			continue
		}
		uniqueK[strings.ToLower(term)]++
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
		term := ""
		if len(m) > 1 {
			term = m[1]
		} else if len(m) > 0 {
			term = m[0]
		}
		if len(term) >= 3 {
			seen[strings.ToLower(term)] = true
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
func filterParagraphs(paragraphs []string, url string) []string {
	documentLanguage, detectedText, confidence := "", "", 0.0
	for _, p := range paragraphs {
		if len(p) > 80 {
			info := whatlanggo.Detect(p)
			documentLanguage, detectedText, confidence = info.Lang.String(), p, info.Confidence
			break
		}
	}
	if documentLanguage == "" && len(paragraphs) > 0 {
		info := whatlanggo.Detect(paragraphs[0])
		documentLanguage, detectedText, confidence = info.Lang.String(), paragraphs[0], info.Confidence
	}
	if documentLanguage != "" && documentLanguage != app.TargetLanguage {
		for i := range paragraphs {
			paragraphs[i] = ""
		}
		recordRejection("non_english", url,
			fmt.Sprintf("detected %s (confidence %.2f), expected %s", documentLanguage, confidence, app.TargetLanguage),
			detectedText)
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
		if ratio < app.MinStopwordRatio {
			recordRejection("stopword_ratio", url,
				fmt.Sprintf("stopword ratio %.3f below %.3f", ratio, app.MinStopwordRatio), p4)
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
		if ratio < app.MinStopwordRatio || ratio > app.MaxStopwordRatio {
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
	existing := app.UniqueSignature[signature]
	if existing == "" {
		app.UniqueSignature[signature] = url
		app.StateDirty = true
		app.CrawlMu.Unlock()
		return true
	}
	app.CrawlMu.Unlock()

	recordRejection("duplicate", url, "signature matches "+existing, "")
	app.Logger.Debug("document rejected", "reason", "duplicate", "url", url, "duplicate_of", existing)
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
	_, nextLink := getNextLink()
	linkSeemsOk(nextLink, false)
	if nextLink == "" {
		fmt.Println("* No more links available in the pool")
		return false
	}

	app.PoolMu.Lock()
	prevState := 0
	if i, ok := app.LinkIndex[nextLink]; ok {
		prevState = app.LPool[i].Status
	}
	app.PoolMu.Unlock()
	if app.StopRequested.Load() {
		return false
	}
	fmt.Printf("\n\n* Downloading url: %s", nextLink)

	content, links, err := downloadCached(nextLink)
	finalStatus := 3
	if err != nil {
		recordQueueEvent("failed", nextLink)
		fmt.Println("\nDownload error: ", strings.Trim(err.Error(), "\n"))
	} else {
		finalStatus = 2
		recordQueueEvent("retrieved", nextLink)
	}
	app.PoolMu.Lock()
	if i, ok := app.LinkIndex[nextLink]; ok {
		app.LPool[i].Status = finalStatus
	}
	app.PoolMu.Unlock()

	if prevState == 4 && finalStatus == 2 {
		addLinksOf(nextLink, links)
	}

	paragraphs := filterParagraphs(splitParagraphs(content), nextLink)
	best := bestParagraph(paragraphs)
	curated := buildCuratedContent(paragraphs)
	docLen := len(tokenize(curated))

	if docLen > app.MaxDocLen {
		recordRejection("too_long", nextLink, fmt.Sprintf("%d tokens > max %d", docLen, app.MaxDocLen), curated)
		app.Logger.Debug("document rejected", "reason", "too_long", "url", nextLink, "tokens", docLen)
		return true
	}
	if docLen < app.MinDocLen {
		recordRejection("too_short", nextLink, fmt.Sprintf("%d tokens < min %d", docLen, app.MinDocLen), curated)
		app.Logger.Debug("document rejected", "reason", "too_short", "url", nextLink, "tokens", docLen)
		return true
	}

	signature, sortedFreqs := documentSignature(curated)
	if !isUniqueDocument(signature, nextLink) {
		return true
	}

	score := rankingScore(curated)
	if score < app.ScoreThreshold {
		recordRejection("low_relevancy", nextLink, fmt.Sprintf("score %.2f < threshold %.2f", score, app.ScoreThreshold), curated)
		app.Logger.Debug("document rejected", "reason", "low_relevancy", "url", nextLink, "score", score)
		return true
	}

	if len(sortedFreqs) > 0 {
		ratio := float64(sortedFreqs[0].Value) / float64(1+docLen)
		if ratio > 0.1 || ratio < 0.002 {
			recordRejection("token_distribution", nextLink, fmt.Sprintf("maxFreq ratio %.4f outside 0.002..0.1", ratio), curated)
			app.Logger.Debug("document rejected", "reason", "token_distribution", "url", nextLink)
			return true
		}
	}

	if err := writeDocument(nextLink, curated, signature, best, score); err != nil {
		recordRejection("output_error", nextLink, err.Error(), "")
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

	if prevState == 0 && finalStatus == 2 {
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
	if viper.IsSet("minStopwordRatio") && !app.minRatioFromCLI {
		app.MinStopwordRatio = viper.GetFloat64("minStopwordRatio")
	}
	if viper.IsSet("maxStopwordRatio") && !app.maxRatioFromCLI {
		app.MaxStopwordRatio = viper.GetFloat64("maxStopwordRatio")
	}
	if viper.IsSet("logRotateDays") {
		app.LogRotateDays = viper.GetInt("logRotateDays")
	}
	if viper.IsSet("logRotateMaxMB") {
		app.LogRotateMaxMB = viper.GetInt("logRotateMaxMB")
	}
	app.DownloadTimeout = time.Duration(viper.GetInt("downloadTimeout")) * time.Second
	if app.DownloadTimeout <= 0 || app.DownloadTimeout > maxRequestTimeout {
		app.DownloadTimeout = maxRequestTimeout
	}
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
	app.NotifyWebhookURL = strings.TrimSpace(viper.GetString("notifyWebhookURL"))
	app.NotifyDesktop = viper.GetBool("notifyDesktop")
	if viper.IsSet("notifyOnCompletion") {
		app.NotifyOnCompletion = viper.GetBool("notifyOnCompletion")
	}
	if viper.IsSet("notifyOnError") {
		app.NotifyOnError = viper.GetBool("notifyOnError")
	}
	app.RejectionNotifyThreshold = viper.GetInt("rejectionNotifyThreshold")
	if app.RejectionNotifyThreshold < 0 {
		app.RejectionNotifyThreshold = 0
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
	if vp.IsSet("minStopwordRatio") && !app.minRatioFromCLI {
		app.MinStopwordRatio = vp.GetFloat64("minStopwordRatio")
	}
	if vp.IsSet("maxStopwordRatio") && !app.maxRatioFromCLI {
		app.MaxStopwordRatio = vp.GetFloat64("maxStopwordRatio")
	}
	app.ScoreThreshold = float64(vp.GetInt("scoreThreshold"))
	app.MaxLinksPerPage = vp.GetInt("maxLinksPerPage")
	if ms := vp.GetInt("crawlDelayMs"); ms > 0 {
		app.CrawlDelay = time.Duration(ms) * time.Millisecond
		if app.CrawlDelay > maxCrawlDelay {
			app.CrawlDelay = maxCrawlDelay
		}
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
	app.ProcessPDF = vp.GetBool("process_pdf")
	if v := vp.GetInt64("pdf_max_bytes"); v > 0 {
		app.PDFMaxBytes = v
	}
	if v := vp.GetInt("pdf_pages_limit"); v > 0 {
		app.PDFPagesLimit = v
	}
	buildRankingRegex()
	if app.MaxLinksPerPage <= 0 {
		app.MaxLinksPerPage = 200
	}

	fmt.Printf("\n\nprofileName: %s", app.ProfileName)
	fmt.Printf("\n\ncuratedDomains: %s", app.CuratedDomains)
	fmt.Printf("\n\nregexLinkOk: %s", app.RegexLinkOk)
	fmt.Printf("\n\nregexRankingKeywords: %s", app.RegexRankingKeywords)
	fmt.Printf("\n\nbootstrapingLinks: %+v", app.BootstrapLinks)
	fmt.Printf("\n\nstopwordRatio: %.3f..%.3f", app.MinStopwordRatio, app.MaxStopwordRatio)
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

const (
	maxRequestTimeout = 30 * time.Second
	maxCrawlDuration  = 10 * time.Minute
	maxCrawlDelay     = 5 * time.Second
	linkStaleTimeout  = 60 * time.Second
)

const appVersion = "1.8.19"

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
		case strings.HasPrefix(arg, "-min-stopword-ratio="), strings.HasPrefix(arg, "--min-stopword-ratio="):
			v, err := strconv.ParseFloat(strings.SplitN(arg, "=", 2)[1], 64)
			if err != nil {
				return fmt.Errorf("invalid -min-stopword-ratio: %w", err)
			}
			app.MinStopwordRatio = v
			app.minRatioFromCLI = true
		case strings.HasPrefix(arg, "-max-stopword-ratio="), strings.HasPrefix(arg, "--max-stopword-ratio="):
			v, err := strconv.ParseFloat(strings.SplitN(arg, "=", 2)[1], 64)
			if err != nil {
				return fmt.Errorf("invalid -max-stopword-ratio: %w", err)
			}
			app.MaxStopwordRatio = v
			app.maxRatioFromCLI = true
		case strings.HasPrefix(arg, "-lint-profile="), strings.HasPrefix(arg, "--lint-profile="):
			app.CLIProfileLint = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-reject-report="), strings.HasPrefix(arg, "--reject-report="):
			app.CLIRejectReport = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-trace-url="), strings.HasPrefix(arg, "--trace-url="):
			app.CLITraceURL = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-trace-file="), strings.HasPrefix(arg, "--trace-file="):
			app.CLITraceFile = strings.SplitN(arg, "=", 2)[1]
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
	if app.ProfileName == "" && app.CLIQuery == "" && !app.CLIExplore && app.CLIGenerate == "" && app.CLIProfileLint == "" && app.CLIRejectReport == "" && app.CLITraceURL == "" && !appGUI {
		return fmt.Errorf("profile argument required")
	}
	return nil
}

// errStop signals that a informational flag was handled and the program should exit cleanly.
var appGUI bool
var crawlCtx context.Context
var cancelCrawl context.CancelFunc
var crawlCtxMu sync.Mutex
var networkRequests atomic.Int64

// networkRequestsTotal includes page downloads and robots.txt fetches.
func networkRequestsTotal() int64 {
	return networkRequests.Load() + robotspolicy.Requests()
}

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
<style>
:root{--bg:#0d1219;--panel:#16212e;--input:#0b1119;--text:#dfe7ee;--muted:#8fa3b8;--accent:#4fc3f7;--border:#2c3e50;--radius:10px;--space:8px;--mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
body.light{--bg:#f4f7fb;--panel:#ffffff;--input:#ffffff;--text:#1a2533;--muted:#5a6b7c;--accent:#0b6fa4;--border:#d7e0e9}
body.hc{--bg:#000000;--panel:#000000;--input:#000000;--text:#ffffff;--muted:#dddddd;--accent:#ffff00;--border:#ffffff}
body.light.hc{--bg:#ffffff;--panel:#ffffff;--input:#ffffff;--text:#000000;--muted:#333333;--accent:#0033aa;--border:#000000}
body.reduce-motion *,body.reduce-motion *::before,body.reduce-motion *::after{animation:none!important;transition:none!important;scroll-behavior:auto!important}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{animation:none!important;transition:none!important;scroll-behavior:auto!important}}
:focus-visible{outline:3px solid var(--accent);outline-offset:2px}
.skip{position:absolute;left:-9999px;top:0;background:var(--accent);color:var(--bg);padding:var(--space);z-index:10}
.skip:focus{left:var(--space)}
*{box-sizing:border-box}body{font-family:Inter,system-ui,-apple-system,Segoe UI,sans-serif;margin:0;background:var(--bg);color:var(--text);font-size:14px;line-height:1.45}
header{padding:calc(var(--space)*1.5) calc(var(--space)*2.5);background:var(--panel);border-bottom:1px solid var(--border);display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:var(--space)}
nav{display:flex;flex-wrap:wrap;background:var(--panel);border-bottom:1px solid var(--border);padding:0 calc(var(--space)*2)}
nav button{background:none;border:0;border-bottom:2px solid transparent;color:var(--muted);padding:calc(var(--space)*1.25) calc(var(--space)*2);cursor:pointer;font-size:14px;transition:color .15s,border-color .15s}
nav button:hover{color:var(--text)}nav button.on{color:var(--accent);border-bottom-color:var(--accent)}
main{padding:calc(var(--space)*2.5)}
.card{background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);padding:calc(var(--space)*2);margin-bottom:calc(var(--space)*2);box-shadow:0 1px 3px rgba(0,0,0,.12)}
textarea,input,select{width:100%;background:var(--input);color:var(--text);border:1px solid var(--border);border-radius:calc(var(--radius)/2);padding:var(--space);margin:6px 0;font-family:var(--mono);font-size:13px}
button.act{background:var(--accent);color:var(--bg);border:0;border-radius:calc(var(--radius)/2);padding:var(--space) calc(var(--space)*2);min-height:36px;cursor:pointer;transition:filter .15s,transform .1s}
button.act:hover{filter:brightness(1.12)}button.act:active{transform:translateY(1px)}
button.act:disabled{opacity:.4;cursor:not-allowed;filter:none}
button.act.state-on{background:var(--accent);color:var(--bg);box-shadow:inset 0 0 0 2px var(--text)}
button.act.state-off{background:var(--input);color:var(--muted);border:1px solid var(--border)}
pre{background:var(--input);padding:var(--space);border-radius:calc(var(--radius)/2);white-space:pre-wrap;max-height:380px;overflow:auto;font-family:var(--mono);font-size:12px}
table{width:100%;border-collapse:collapse}td,th{padding:var(--space);border-bottom:1px solid var(--border);text-align:left}
.row{display:flex;flex-wrap:wrap;gap:var(--space)}.row>*{flex:1 1 130px;min-width:110px}
.muted{color:var(--muted)}.kpi{font-size:20px;font-family:var(--mono);font-weight:600;color:var(--accent)}
.about-row{display:flex;gap:calc(var(--space)*3);align-items:flex-start}.about-info{flex:1 1 420px;min-width:0}.about-qr{flex:0 0 220px;text-align:center}.kpi-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(118px,1fr));gap:var(--space)}
.kpi-card{background:var(--input);border:1px solid var(--border);border-radius:calc(var(--radius)/2);padding:var(--space) calc(var(--space)*1.5);min-width:0}
.kpi-label{display:block;color:var(--muted);font-size:11px;text-transform:uppercase;letter-spacing:.04em;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.kpi-value{display:block;font-family:var(--mono);font-size:19px;font-weight:600;color:var(--accent);line-height:1.25}
.bar{height:10px;background:var(--accent);border-radius:5px;margin:3px 0}
#c-download:not([hidden]){display:inline-block;padding:var(--space) calc(var(--space)*2);background:var(--input);border:1px solid var(--border);border-radius:calc(var(--radius)/2);color:var(--accent)}
@media(max-width:760px){.about-row{flex-direction:column}.about-qr{flex:1 1 auto}}header,nav{padding-left:var(--space);padding-right:var(--space)}main{padding:var(--space)}.row>*{flex:1 1 100%}nav button{padding:var(--space)}}
</style></head><body>
<a class="skip" href="#main">Skip to content</a>
<header><b>goCrawler Workbench</b> <span class="muted" id="status">ready</span><button class="act state-off" id="btn-theme" onclick="toggleTheme()" aria-label="Toggle dark and light theme">Theme: dark</button><button class="act state-off" id="btn-contrast" onclick="toggleContrast()" aria-label="Toggle high contrast" aria-pressed="false">○ Contrast OFF</button><button class="act state-off" id="btn-motion" onclick="toggleMotion()" aria-label="Toggle reduced motion" aria-pressed="false">○ Motion OFF</button></header>
<nav role="tablist" aria-label="Workbench sections"><button class="on" data-tab="prof">Profiles</button><button data-tab="dash">Crawler launcher</button><button data-tab="keys">Keyword lab</button><button data-tab="corpus">Corpus</button><button data-tab="prompt">Prompt generator</button><button data-tab="urls">URL Seeds</button><button data-tab="about">About</button></nav><main id="main">
<section id="dash" hidden><div class="card"><div class="kpi-grid"><div class="kpi-card"><span class="kpi-label">Pages visited</span><span class="kpi-value" id="s-visits">0</span></div><div class="kpi-card"><span class="kpi-label">Cache hits</span><span class="kpi-value" id="s-cache">0</span></div><div class="kpi-card"><span class="kpi-label">Online</span><span class="kpi-value" id="s-online">0</span></div><div class="kpi-card"><span class="kpi-label">Download ms</span><span class="kpi-value" id="s-ms">0</span></div><div class="kpi-card"><span class="kpi-label">Accepted</span><span class="kpi-value" id="s-accepted">0</span></div><div class="kpi-card"><span class="kpi-label">Not accepted</span><span class="kpi-value" id="s-rejected">0</span></div><div class="kpi-card"><span class="kpi-label">Crawls running</span><span class="kpi-value" id="s-running">0</span></div><div class="kpi-card"><span class="kpi-label">Net reqs</span><span class="kpi-value" id="s-net">0</span></div><div class="kpi-card"><span class="kpi-label">Cache I/O</span><span class="kpi-value" id="s-io">0</span></div><div class="kpi-card"><span class="kpi-label">URLs/s</span><span class="kpi-value" id="s-rate">0</span></div><div class="kpi-card"><span class="kpi-label">Avg ms</span><span class="kpi-value" id="s-lat">0</span></div><div class="kpi-card"><span class="kpi-label">Queue</span><span class="kpi-value" id="s-queue">0</span></div><div class="kpi-card"><span class="kpi-label">Go routines</span><span class="kpi-value" id="s-goroutines">0</span></div></div></div><div class="card"><h3>Why pages were not accepted</h3><div id="reasons"></div><div id="reject-chart"></div><div id="reason-detail"></div><p class="muted">Filter decisions are counted per paragraph/link, so totals can exceed the number of pages.</p></div>
<div class="card"><h3>URL queue</h3><p class="muted">Pending: <b id="q-total">0</b>. States: requested → retrieved/failed → removed.</p><div id="queue-list"></div></div>
<div class="card"><h3>Run a crawl</h3><input id="run-profile" placeholder="profile name"><select id="w-workers"><option value="1">1 worker</option><option value="2">2 workers</option><option value="4" selected>4 workers</option><option value="8">8 workers</option></select><button id="btn-start" class="act" onclick="startCrawl()">Start</button> <button id="btn-stop" class="act" onclick="stopCrawl()" disabled>Stop</button><pre id="run-out"></pre></div></section>
<section id="prof"><div class="card"><select id="p-list" onchange="loadProfile()"></select><textarea id="p-text" rows="22"></textarea><button class="act" onclick="saveProfile()">Save profile</button></div></section>
<section id="corpus" hidden><div class="card"><h3>Search the curated corpus</h3><p class="muted">Full-text search over the accepted documents (SQLite FTS5).</p><input id="q" placeholder="FTS5 query"><button class="act" onclick="search()">Search</button><div id="docs"></div></div>
<div class="card"><h3>Save corpus for a profile</h3><p class="muted">Select a profile and a scope, then export the curated documents whose URL and text match it to a TXT file.</p><select id="c-profile"></select><select id="c-scope"><option value="all">All accumulated</option><option value="last">Last crawl only</option><option value="today">Today only</option></select><button class="act" onclick="saveCorpus()">Save corpus</button> <a id="c-download" hidden>Download TXT</a><pre id="c-out"></pre></div></section>
<section id="prompt" hidden><div class="card"><h3>AI research prompt</h3><p class="muted">Built from the selected profile keywords, seed URLs and curated text.</p><select id="pr-profile"></select><button class="act" onclick="generatePrompt()">Generate prompt</button><textarea id="pr-out" rows="18"></textarea><button class="act" onclick="copyPrompt()">Copy</button></div></section>
<section id="about" hidden><div class="about-row"><div class="card about-info"><h3 id="a-name">goCrawler Workbench</h3><p class="muted" id="a-desc"></p><p>Version <b id="a-version"></b> — <span id="a-date"></span></p><p id="a-copyright"></p><p>License: <b id="a-license"></b> · Go: <span id="a-go"></span> · Platform: <span id="a-platform"></span></p><p>Contact: <a id="a-author" href="#"></a> · <a id="a-linkedin" href="#" target="_blank" rel="noopener">LinkedIn</a></p><p><a id="a-repo" href="#" target="_blank" rel="noopener">Repository</a> · <a id="a-issues" href="#" target="_blank" rel="noopener">Issues</a> · <a id="a-changelog" href="#" target="_blank" rel="noopener">Changelog</a> · <a id="a-roadmap" href="#" target="_blank" rel="noopener">Roadmap</a></p><p>Components: <span id="a-components" class="muted"></span></p></div><div class="card about-qr"><h3>Scan to open the repository</h3><img id="a-qr" alt="QR code of the repository" style="max-width:180px"></div></div></section>
<section id="urls" hidden><div class="card"><h3>URL Seeds</h3><p class="muted">Seeds in profile: <b id="u-count">0</b> URLs. Paste one URL per line or URLs mixed with text, tabs and line breaks; duplicates are ignored. Changes are written to the profile YAML.</p><select id="u-profile"></select><textarea id="u-text" rows="12" placeholder="https://example.org/page"></textarea><div><button class="act" onclick="addURLs()">Add URLs</button> <button class="act" onclick="replaceURLs()">Replace all</button> <button class="act" onclick="removeURLs()">Remove selected</button></div><div id="u-list"></div></div></section>
<section id="keys" hidden><div class="card"><p class="muted">Extract topic keywords from the curated corpus and feed them back into a profile.</p><input id="k-profile" placeholder="profile name"><button class="act" onclick="extract()">Extract</button><div id="k-out"></div><button class="act" onclick="applyKeywords()">Apply to profile</button> <button class="act" onclick="applyAndRun()">Apply &amp; Re-run</button></div></section>
</main><script>
const $=id=>document.getElementById(id);
const navButtons=()=>[...document.querySelectorAll('nav button')];
function applyTheme(light){document.body.classList.toggle('light',light);localStorage.setItem('gc-theme',light?'light':'dark');const b=$('btn-theme');if(b){b.classList.toggle('state-on',light);b.classList.toggle('state-off',!light);b.textContent='Theme: '+(light?'light':'dark')}}
function toggleTheme(){applyTheme(!document.body.classList.contains('light'))}
applyTheme(localStorage.getItem('gc-theme')==='light');
function setPressed(id,on,label){const b=$(id);if(b){b.setAttribute('aria-pressed',on?'true':'false');b.classList.toggle('state-on',on);b.classList.toggle('state-off',!on);b.textContent=(on?'● ':'○ ')+label+' '+(on?'ON':'OFF')}}
function applyContrast(on){document.body.classList.toggle('hc',on);localStorage.setItem('gc-contrast',on?'on':'off');setPressed('btn-contrast',on,'Contrast')}
function applyMotion(reduce){document.body.classList.toggle('reduce-motion',reduce);localStorage.setItem('gc-motion',reduce?'reduce':'full');setPressed('btn-motion',reduce,'Motion')}
applyContrast(localStorage.getItem('gc-contrast')==='on');
applyMotion(localStorage.getItem('gc-motion')==='reduce'||(!localStorage.getItem('gc-motion')&&window.matchMedia&&window.matchMedia('(prefers-reduced-motion: reduce)').matches));
function toggleContrast(){applyContrast(!document.body.classList.contains('hc'))}
function toggleMotion(){applyMotion(!document.body.classList.contains('reduce-motion'))}

function selectTab(index){const b=navButtons()[index];if(b)b.click()}
navButtons().forEach((b,i)=>{b.setAttribute('role','tab');b.setAttribute('aria-selected',b.classList.contains('on')?'true':'false');b.title='Alt+'+(i+1);b.onclick=()=>{navButtons().forEach(x=>{x.classList.remove('on');x.setAttribute('aria-selected','false')});b.classList.add('on');b.setAttribute('aria-selected','true');['prof','dash','keys','corpus','prompt','urls','about'].forEach(t=>$(t).hidden=t!==b.dataset.tab)}});
document.addEventListener('keydown',e=>{if(e.altKey&&e.key>='1'&&e.key<='7'){e.preventDefault();selectTab(parseInt(e.key,10)-1)}else if((e.ctrlKey||e.metaKey)&&e.key.toLowerCase()==='k'){e.preventDefault();document.querySelector('[data-tab=corpus]').click();setTimeout(()=>$('q').focus(),0)}});
async function json(u,o){const r=await fetch(u,o);if(!r.ok)throw new Error((await r.text()).slice(0,200));return r.json()}
async function stats(){const s=await json('/api/stats');$('s-visits').textContent=s.visits;$('s-cache').textContent=s.cache;$('s-online').textContent=s.online;$('s-ms').textContent=s.download_ms;$('s-accepted').textContent=s.accepted;$('s-rejected').textContent=s.not_accepted;$('s-running').textContent=s.running_count;$('s-net').textContent=s.network;$('s-io').textContent=s.cache_io;$('s-rate').textContent=(s.rate||0).toFixed(1);$('s-lat').textContent=(s.avg_latency||0).toFixed(0);$('s-queue').textContent=s.queue;$('s-goroutines').textContent=s.goroutines;$('btn-start').disabled=s.running;$('btn-stop').disabled=!s.running;$('status').textContent=s.error?('error: '+s.error):(s.running?(s.stopping?'stopping...':('running ('+s.running_count+')')):'ready');const entries=Object.entries(s.rejections||{}).sort((a,b)=>b[1]-a[1]);$('reasons').innerHTML=entries.length?('<table><tr><th>Reason</th><th>Count</th></tr>'+entries.map(e=>'<tr class=clickable onclick="showReason(\''+e[0]+'\')"><td>'+e[0]+'</td><td>'+e[1]+'</td></tr>').join('')+'</table>'):'<span class=muted>No filter decisions yet</span>'}
async function profiles(){const p=await json('/api/profiles');const last=await json('/api/last-profile');const opts=p.map(x=>'<option>'+x+'</option>').join('');$('p-list').innerHTML=opts;$('pr-profile').innerHTML=opts;$('u-profile').innerHTML=opts;if($('c-profile'))$('c-profile').innerHTML=opts;const match=p.find(x=>last.profile&&x.toLowerCase()===last.profile.toLowerCase());if(match){$('p-list').value=match;$('pr-profile').value=match;$('u-profile').value=match;if($('c-profile'))$('c-profile').value=match;}loadProfile()}
async function loadProfile(){const n=$('p-list').value;if(!n)return;const d=await json('/api/profile?name='+encodeURIComponent(n));$('p-text').value=d.content;$('k-profile').value=n;$('run-profile').value=n;if($('pr-profile'))$('pr-profile').value=n;if($('u-profile')){$('u-profile').value=n;loadURLs()}}
async function saveProfile(){const n=$('p-list').value;await fetch('/api/profile',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:n,content:$('p-text').value})});$('status').textContent='saved'}
async function search(){const d=await json('/api/search?q='+encodeURIComponent($('q').value));$('docs').innerHTML=d.results.map(x=>'<div class="card"><b>'+x.url+'</b><pre>'+x.snippet+'</pre><button class="act" onclick="copyText(this)">Copy</button></div>').join('')}
async function saveCorpus(){const n=$('c-profile').value;if(!n){$('c-out').textContent='Select a profile first';return}const scope=$('c-scope').value;try{const r=await fetch('/api/corpus/save',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile:n,scope:scope})});const d=await r.json();if(!r.ok)throw new Error(d.error||JSON.stringify(d));$('c-out').textContent=scope+' · '+d.documents+' documents, '+d.characters+' characters\n'+d.file;$('c-download').href=d.download;$('c-download').textContent='Download '+d.file;$('c-download').hidden=false}catch(e){$('c-out').textContent='Error: '+e.message}}
let extracted=[];let removed=[];async function extract(){removed=[];const n=$('k-profile').value||$('p-list').value;const d=await json('/api/keywords?profile='+encodeURIComponent(n));extracted=d.keywords.slice();$('k-out').innerHTML='<table><tr><th>Word</th><th>Count</th><th>Docs</th><th>Score</th></tr>'+(d.details||[]).map(x=>'<tr><td>'+x.word+' <button class=act onclick="removeKeyword(\''+x.word+'\')">x</button></td><td>'+x.count+'</td><td>'+x.docs+'</td><td>'+x.score.toFixed(2)+'</td></tr>').join('')+'</table>'}function removeKeyword(w){removed.push(w);extracted=extracted.filter(k=>k!==w);const row=[...$('k-out').querySelectorAll('tr')].find(r=>r.textContent.startsWith(w));if(row)row.remove()}
async function applyKeywords(){const n=$('k-profile').value;const kws=extracted;const r=await fetch('/api/keywords/apply',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile:n,keywords:kws,removed:removed})});const res=await r.json();$('status').textContent=res.status+' ('+res.keywords+' keywords)';document.querySelector('[data-tab=prof]').click();profiles()}
function parseURLs(){const text=$('u-text').value;const found=text.match(/https?:\/\/[^\s<>"']+/gi)||[];const seen=new Set(),out=[];for(let raw of found){const u=raw.replace(/^[<(\["']+/,'').replace(/[>"')\]},.;:!?]+$/,'');if(/^https?:\/\//i.test(u)&&!seen.has(u)){seen.add(u);out.push(u)}}return out}
async function loadURLs(){const n=$('u-profile').value||$('p-list').value;if(!n)return;const d=await json('/api/urls?profile='+encodeURIComponent(n));$('u-text').value=d.urls.join('\n');$('u-count').textContent=d.urls.length;$('u-list').innerHTML=d.urls.map((u,i)=>'<label><input type=checkbox value="'+i+'"> '+u+'</label>').join('<br>')}
async function saveURLs(mode,remove){const n=$('u-profile').value||$('p-list').value;const urls=parseURLs();const body={profile:n,urls:urls,mode:mode,remove:remove||[]};const r=await fetch('/api/urls',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});const res=await r.json();$('status').textContent=(res.status||res.error)+' ('+res.urls+' URLs)';loadURLs();loadProfile()}
async function addURLs(){await saveURLs('append',[])}
async function replaceURLs(){await saveURLs('replace',[])}
async function removeURLs(){const n=$('u-profile').value||$('p-list').value;const d=await json('/api/urls?profile='+encodeURIComponent(n));const picked=[...$('u-list').querySelectorAll('input:checked')].map(x=>d.urls[parseInt(x.value)]);await saveURLs('append',picked)}
async function applyAndRun(){if(!confirm('Apply extracted keywords and start a new crawl?'))return;await applyKeywords();$('run-profile').value=$('k-profile').value;startCrawl()}
async function loadAbout(){const a=await json('/api/about');for(const [id,key] of [['a-name','name'],['a-version','version'],['a-date','release_date'],['a-copyright','copyright'],['a-license','license'],['a-go','go_version'],['a-platform','platform'],['a-desc','description']]){if($(id))$(id).textContent=a[key]}if($('a-components'))$('a-components').textContent=(a.components||[]).join(', ');if($('a-author')){$('a-author').href='mailto:'+a.email;$('a-author').textContent=a.author}if($('a-linkedin'))$('a-linkedin').href=a.linkedin;for(const [id,key] of [['a-repo','repo'],['a-issues','issues'],['a-changelog','changelog'],['a-roadmap','roadmap']]){if($(id)){$(id).href=a[key];$(id).textContent=key}}if($('a-qr'))$('a-qr').src=a.qr}
async function generatePrompt(){const n=$('pr-profile').value||$('p-list').value||$('run-profile').value;try{const d=await json('/api/prompt?profile='+encodeURIComponent(n));$('pr-out').value=d.prompt;$('status').textContent='prompt ready ('+d.prompt.length+' chars)'}catch(e){$('pr-out').value='Error: '+e.message;$('status').textContent='prompt error'}}
function copyPrompt(){navigator.clipboard.writeText($('pr-out').value)}
async function startCrawl(){const r=await fetch('/api/crawl',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile:$('run-profile').value,workers:parseInt($('w-workers').value)})});$('run-out').textContent=JSON.stringify(await r.json());stats()}
async function stopCrawl(){const r=await fetch('/api/crawl/stop',{method:'POST'});$('status').textContent=(await r.json()).status;stats()}
async function loadQueue(){const q=await json('/api/queue');$('q-total').textContent=q.total;const rows=q.recent.slice(-12).reverse().map(e=>'<div><span class=muted>'+new Date(e.time).toLocaleTimeString()+'</span> <b>'+e.state+'</b> '+e.url+'</div>');const pend=q.pending.slice(0,20).map(p=>'<div><span class=muted>pending</span> '+p.url+'</div>');$('queue-list').innerHTML=rows.join('')+pend.join('')||'<span class=muted>Queue empty</span>'}
async function showReason(reason){const d=await json('/api/rejections?reason='+encodeURIComponent(reason));if(!d.events||!d.events.length){$('reason-detail').innerHTML='<p class=muted>No cases recorded for '+reason+'</p>';return}$('reason-detail').innerHTML='<h4>Last '+d.events.length+' cases: '+reason+'</h4>'+d.events.slice().reverse().map(e=>'<div class=card><b><a href="'+e.url+'" target="_blank" rel="noopener">'+e.url+'</a></b><div>'+(e.detail||'')+'</div>'+(e.text?'<pre>'+e.text.slice(0,600)+'</pre>':'')+'<span class=muted>'+new Date(e.time).toLocaleString()+'</span></div>').join('')}
function copyText(b){navigator.clipboard.writeText(b.previousElementSibling.textContent)}
setInterval(()=>{stats();loadQueue()},1500);stats();loadQueue();profiles();loadAbout()
</script></body></html>`

var guiStopwords = map[string]bool{}
var guiStopwordList = `a about above after again against all almost also although always am among an and another any anybody anyone anything are around as at back be because been before being below between both but by can cannot cant could did do does doing done down during each either else enough etc even ever every few first for from further get gets getting had has have having he her here hers herself him himself his how however i if in into is it its itself just last later least less let like made make many may me might more most much must my myself near need neither never new no nor not now of off often on once one only or other others our ours ourselves out over own perhaps rather same she should since so some someone something still such than that the their theirs them themselves then there therefore these they this those though through to too toward under until up upon us use used very was we were what whatever when whenever where whether which while who whoever why will with within without would yes yet you your yours yourself yourselves`

func init() {
	for _, word := range strings.Fields(guiStopwordList) {
		guiStopwords[word] = true
	}
}

var guiCrawling atomic.Bool

// freshCrawlContext creates a cancellable context for a single crawl.
func freshCrawlContext() {
	crawlCtxMu.Lock()
	defer crawlCtxMu.Unlock()
	if cancelCrawl != nil {
		cancelCrawl()
	}
	crawlCtx, cancelCrawl = context.WithTimeout(context.Background(), maxCrawlDuration)
}

// guiCrawlContext returns the active crawl context.
func guiCrawlContext() context.Context {
	crawlCtxMu.Lock()
	defer crawlCtxMu.Unlock()
	return crawlCtx
}

// launchGUICrawl runs a crawl inside the GUI process so its metrics are live.
func launchGUICrawl(profile string) error {
	resolved, err := resolveProfileName(profile)
	if err != nil {
		return err
	}
	if !guiCrawling.CompareAndSwap(false, true) {
		return fmt.Errorf("a crawl is already running")
	}
	freshCrawlContext()
	go func() {
		defer guiCrawling.Store(false)
		app.ResetForProfile(resolved)
		startIndex, cleanup, err := setupRuntime(resolved)
		if err != nil {
			app.GUILastError = err.Error()
			notifyError(err.Error())
			return
		}
		defer cleanup()
		final := crawlLoop(startIndex)
		reportResults(final)
	}()
	return nil
}

// crawlRate returns processed pages per second since the crawl started.
func crawlRate() float64 {
	if app.CrawlStartedAt.IsZero() {
		return 0
	}
	seconds := time.Since(app.CrawlStartedAt).Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(app.CacheHits+app.OnlineDownloads) / seconds
}

// averageLatency returns the mean download time in milliseconds.
func averageLatency() float64 {
	if app.OnlineDownloads == 0 {
		return 0
	}
	return float64(app.DownloadMS) / float64(app.OnlineDownloads)
}

// queueDepth reports how many links remain in the pool.
func queueDepth() int {
	app.PoolMu.Lock()
	defer app.PoolMu.Unlock()
	return len(app.LPool)
}

// guiHandler builds the HTTP handler for the workbench; exported for tests.
func guiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, guiHTML)
	})
	mux.HandleFunc("/api/prompt", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Query().Get("profile"))
		if name == "" || name == "." {
			name = lastExecutedProfile()
		}
		if name == "" {
			if files, _ := filepath.Glob("profiles/*.yaml"); len(files) > 0 {
				name = strings.TrimSuffix(filepath.Base(files[0]), ".yaml")
			}
		}
		if name == "" {
			http.Error(w, "profile required", 400)
			return
		}
		resolved, err := resolveProfileName(name)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		path := "profiles/" + resolved + ".yaml"
		vp := viper.New()
		vp.SetConfigFile(path)
		if err := vp.ReadInConfig(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var b strings.Builder
		b.WriteString("You are a research assistant. Using the profile below, perform a deep and exhaustive investigation of additional, more detailed sources that can broaden the scope of this research. Be thorough. I need many reference pages and additional content.\n\n")
		b.WriteString("PROFILE: " + name + "\n\n")
		b.WriteString("KEYWORDS (URL filters): " + strings.TrimSpace(vp.GetString("linkOk")) + "\n\n")
		b.WriteString("RELEVANCE TERMS: " + strings.TrimSpace(vp.GetString("regexRankingKeywords")) + "\n\n")
		b.WriteString("SEED URLS:\n")
		for _, seed := range vp.GetStringSlice("bootstrapingLinks") {
			b.WriteString("- " + seed + "\n")
		}
		b.WriteString("\nCURATED CONTENT (complete documents):\n")
		for i, doc := range corpusDocuments(1000) {
			text := strings.TrimSpace(doc["text"])
			b.WriteString(fmt.Sprintf("[%d] %s\n%s\n\n", i+1, doc["url"], text))
		}
		b.WriteString("\nTASK: Identify further high-value pages, articles, documentation and datasets that match these keywords and extend the curated content. Explain briefly why each source is relevant. All results, all pages you provide and the entire report must always be written in English. The result must be a markdown file to be downloaded\n")
		writeJSON(w, map[string]string{"prompt": b.String()})
	})
	mux.HandleFunc("/api/urls", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Query().Get("profile"))
		if r.Method == http.MethodPost {
			var body struct {
				Profile string   `json:"profile"`
				Urls    []string `json:"urls"`
				Mode    string   `json:"mode"`
				Remove  []string `json:"remove"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			resolved, err := resolveProfileName(body.Profile)
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
			path := "profiles/" + resolved + ".yaml"
			b, err := os.ReadFile(path)
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
			existing := profileSeedURLs(string(b))
			removed := make(map[string]bool)
			for _, u := range body.Remove {
				removed[strings.TrimSpace(u)] = true
			}
			kept := []string{}
			for _, u := range existing {
				if !removed[u] {
					kept = append(kept, u)
				}
			}
			added := normalizeSeedURLs(body.Urls)
			var final []string
			if body.Mode == "replace" && len(removed) == 0 {
				final = added
			} else {
				seen := make(map[string]bool)
				for _, u := range kept {
					seen[u] = true
				}
				final = append(final, kept...)
				for _, u := range added {
					if !seen[u] {
						seen[u] = true
						final = append(final, u)
					}
				}
			}
			text := replaceSeedBlock(string(b), final)
			if err := os.WriteFile(path, []byte(text), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]interface{}{"status": "saved", "urls": len(final), "profile": resolved})
			return
		}
		resolved, err := resolveProfileName(name)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		b, err := os.ReadFile("profiles/" + resolved + ".yaml")
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		writeJSON(w, map[string]interface{}{"profile": resolved, "urls": profileSeedURLs(string(b))})
	})
	mux.HandleFunc("/api/rejections", func(w http.ResponseWriter, r *http.Request) {
		reason := r.URL.Query().Get("reason")
		app.RejectionMu.Lock()
		events := append([]rejectionEvent{}, app.RejectionEvents[reason]...)
		app.RejectionMu.Unlock()
		writeJSON(w, map[string]interface{}{"reason": reason, "events": events})
	})
	mux.HandleFunc("/api/queue", func(w http.ResponseWriter, r *http.Request) {
		app.PoolMu.Lock()
		pending := make([]map[string]string, 0, len(app.LPool))
		for _, link := range app.LPool {
			state := "pending"
			if link.Status == 1 {
				state = "requested"
			} else if link.Status == 4 {
				state = "bootstrap"
			}
			pending = append(pending, map[string]string{"url": link.URL, "state": state})
		}
		app.PoolMu.Unlock()
		app.QueueMu.Lock()
		recent := append([]queueEvent{}, app.QueueEvents...)
		app.QueueMu.Unlock()
		writeJSON(w, map[string]interface{}{"total": len(pending), "pending": pending, "recent": recent})
	})
	mux.HandleFunc("/api/about", func(w http.ResponseWriter, r *http.Request) {
		repoURL := "https://github.com/computerphysicslab/goCrawler"
		png, err := qrcode.Encode(repoURL, qrcode.Medium, 180)
		qr := ""
		if err == nil {
			qr = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
		writeJSON(w, map[string]interface{}{
			"name":         "goCrawler Workbench",
			"version":      appVersion,
			"release_date": "2026-11-18",
			"copyright":    "Copyright (c) 2020-2026 Juan Ignacio Pérez Sacristán",
			"license":      "MIT",
			"author":       "computerphysicslab@gmail.com",
			"email":        "computerphysicslab@gmail.com",
			"linkedin":     "https://www.linkedin.com/in/semanticwebarchitect/",
			"description":  "Topic-restricted crawler that builds curated English text corpora with keyword, language, length and relevance filters.",
			"repo":         repoURL,
			"issues":       repoURL + "/issues",
			"changelog":    repoURL + "/blob/master/CHANGELOG.md",
			"roadmap":      repoURL + "/blob/master/roadmap.md",
			"go_version":   runtime.Version(),
			"platform":     runtime.GOOS + "/" + runtime.GOARCH,
			"goroutines":   runtime.NumGoroutine(),
			"components": []string{"Go standard library (net/http, database/sql, log/slog)",
				"modernc.org/sqlite", "github.com/temoto/robotstxt", "github.com/abadojack/whatlanggo",
				"jaytaylor.com/html2text", "github.com/jackdanger/collectlinks", "github.com/spf13/viper",
				"github.com/skip2/go-qrcode", "github.com/kljensen/snowball", "github.com/ledongthuc/pdf",
				"github.com/gomodule/redigo", "github.com/computerphysicslab/goPackages"},
			"qr": qr,
		})
	})
	mux.HandleFunc("/api/last-profile", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"profile": lastExecutedProfile()})
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
			resolved, err := resolveProfileName(name)
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
			b, err := os.ReadFile("profiles/" + resolved + ".yaml")
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
			writeJSON(w, map[string]string{"name": resolved, "content": string(b)})
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
			"rate":          crawlRate(),
			"avg_latency":   averageLatency(),
			"queue":         queueDepth(),
			"goroutines":    runtime.NumGoroutine(),
			"network":       networkRequestsTotal(),
			"cache_io":      storelib.Operations(),
			"running":       running,
			"running_count": runningCount,
			"stopping":      app.StopRequested.Load(),
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
	mux.HandleFunc("/api/corpus/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Profile string `json:"profile"`
			Scope   string `json:"scope"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		info, err := saveProfileCorpus(body.Profile, body.Scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("/api/corpus/download", func(w http.ResponseWriter, r *http.Request) {
		resolved, err := resolveProfileName(r.URL.Query().Get("profile"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = "all"
		}
		safe := safeProfileFilename(resolved) + "-" + scope
		path := filepath.Join("output", "corpus-"+safe+".txt")
		content, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		name := "corpus-" + safe + ".txt"
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Write(content)
	})
	mux.HandleFunc("/api/keywords", func(w http.ResponseWriter, r *http.Request) {
		excluded := make(map[string]bool)
		if name := filepath.Base(r.URL.Query().Get("profile")); name != "" && name != "." {
			if resolved, err := resolveProfileName(name); err == nil {
				if b, err := os.ReadFile("profiles/" + resolved + ".yaml"); err == nil {
					for _, word := range yamlBlockAlternatives(string(b), "excludedKeywords:") {
						for _, variant := range keywordFamily(word) {
							excluded[variant] = true
						}
						excluded[strings.ToLower(word)] = true
					}
				}
			}
		}
		docs := corpusDocuments(500)
		type group struct {
			forms map[string]int
			docs  int
		}
		groups := make(map[string]*group)
		for _, d := range docs {
			seen := make(map[string]bool)
			for _, tok := range tokenize(d["text"]) {
				lower := strings.ToLower(tok)
				if len(tok) <= 3 || guiStopwords[lower] || excluded[lower] {
					continue
				}
				stem := snowballeng.Stem(lower, false)
				g, ok := groups[stem]
				if !ok {
					g = &group{forms: make(map[string]int)}
					groups[stem] = g
				}
				g.forms[lower]++
				seen[stem] = true
			}
			for stem := range seen {
				groups[stem].docs++
			}
		}
		type item struct {
			Word     string   `json:"word"`
			Count    int      `json:"count"`
			Docs     int      `json:"docs"`
			Score    float64  `json:"score"`
			Variants []string `json:"variants,omitempty"`
		}
		items := []item{}
		for _, g := range groups {
			representative, best := "", 0
			total := 0
			variants := []string{}
			for form, count := range g.forms {
				total += count
				variants = append(variants, form)
				if count > best || (count == best && (representative == "" || len(form) < len(representative))) {
					representative, best = form, count
				}
			}
			sort.Strings(variants)
			baseline := corpusfreqlib.Freq(representative)
			score := float64(total) * math.Log(1+float64(g.docs)) / math.Log(2+float64(baseline))
			items = append(items, item{representative, total, g.docs, score, variants})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Score == items[j].Score {
				return items[i].Word < items[j].Word
			}
			return items[i].Score > items[j].Score
		})
		words := []string{}
		details := []item{}
		for i, it := range items {
			if i >= 40 {
				break
			}
			words = append(words, it.Word)
			details = append(details, it)
		}
		writeJSON(w, map[string]interface{}{"keywords": words, "details": details})
	})

	mux.HandleFunc("/api/keywords/apply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Profile  string   `json:"profile"`
			Keywords []string `json:"keywords"`
			Removed  []string `json:"removed"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		name, err := resolveProfileName(body.Profile)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		path := "profiles/" + name + ".yaml"
		b, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		text := string(b)

		existing := listUnique(append(yamlBlockAlternatives(text, "linkOk:"),
			yamlBlockAlternatives(text, "regexRankingKeywords:")...))
		removed := make(map[string]bool)
		for _, word := range body.Removed {
			for _, variant := range keywordFamily(word) {
				removed[strings.ToLower(variant)] = true
			}
		}
		for _, word := range body.Keywords {
			for _, variant := range keywordFamily(word) {
				removed[strings.ToLower(variant)] = false
			}
		}
		combined := []string{}
		for _, word := range append(existing, body.Keywords...) {
			if !removed[strings.ToLower(word)] && !guiStopwords[strings.ToLower(word)] {
				combined = append(combined, strings.ToLower(word))
			}
		}
		final := expandKeywords(listUnique(combined))
		if len(final) == 0 {
			http.Error(w, "refusing to write an empty keyword set", 400)
			return
		}
		text = replaceYamlBlock(text, "linkOk:", strings.Join(final, "|"))
		text = replaceYamlBlock(text, "regexRankingKeywords:", `(?i)\W(`+strings.Join(final, "|")+`)\W`)
		excludedSet := make(map[string]bool)
		for _, word := range yamlBlockAlternatives(text, "excludedKeywords:") {
			excludedSet[word] = true
		}
		for _, word := range body.Removed {
			for _, variant := range keywordFamily(word) {
				excludedSet[variant] = true
			}
		}
		for _, word := range final {
			delete(excludedSet, word)
		}
		if len(excludedSet) > 0 {
			blockNames := []string{}
			for word := range excludedSet {
				blockNames = append(blockNames, word)
			}
			sort.Strings(blockNames)
			if !strings.Contains(text, "excludedKeywords:") {
				text += "\nexcludedKeywords: |\n  " + strings.Join(blockNames, "|") + "\n"
			} else {
				text = replaceYamlBlock(text, "excludedKeywords:", strings.Join(blockNames, "|"))
			}
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"status": "applied", "keywords": len(final)})
	})

	mux.HandleFunc("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		var body struct {
			Profile string `json:"profile"`
			Workers int    `json:"workers"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		name := filepath.Base(body.Profile)
		if name == "" || name == "." {
			http.Error(w, "profile required", 400)
			return
		}
		if body.Workers >= 1 && body.Workers <= 32 {
			app.CLIWorkers = body.Workers
		}
		if err := launchGUICrawl(name); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, map[string]string{"status": "started", "profile": name})
	})
	mux.HandleFunc("/api/crawl/stop", func(w http.ResponseWriter, r *http.Request) {
		app.StopRequested.Store(true)
		app.StateDirty = true
		crawlCtxMu.Lock()
		if cancelCrawl != nil {
			cancelCrawl()
		}
		crawlCtxMu.Unlock()
		writeJSON(w, map[string]string{"status": "stopping"})
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

// resolveProfileName finds an existing profile ignoring case and spaces.
func resolveProfileName(name string) (string, error) {
	files, _ := filepath.Glob("profiles/*.yaml")
	normalize := func(value string) string {
		return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(value))
	}
	target := normalize(filepath.Base(name))
	for _, file := range files {
		base := strings.TrimSuffix(filepath.Base(file), ".yaml")
		if normalize(base) == target {
			return base, nil
		}
	}
	return "", fmt.Errorf("profile not found: %s", name)
}

// knownProfileKeys lists every key understood by yamlInitSpecific. Unknown keys
// are reported by `lintProfile` because Viper silently ignores them.
var knownProfileKeys = map[string]bool{
	"bootstrapinglinks":     true,
	"crawldelayms":          true,
	"curateddomains":        true,
	"excludedkeywords":      true,
	"language":              true,
	"linkok":                true,
	"maxdoclen":             true,
	"maxlinksperpage":       true,
	"maxstopwordratio":      true,
	"mindoclen":             true,
	"minlanguageconfidence": true,
	"minstopwordratio":      true,
	"pdf_max_bytes":         true,
	"pdf_pages_limit":       true,
	"process_pdf":           true,
	"rankingstrategy":       true,
	"regexrankingkeywords":  true,
	"scorethreshold":        true,
	"synonyms":              true,
}

// lintProfile checks a profile file without starting a crawl: required keys,
// regexes, seed URLs, numeric ranges and unknown keys.
func lintProfile(name string) error {
	path := "./profiles/" + name + ".yaml"
	if !iolib.FileExists(path) {
		path = "./" + name + ".yaml"
	}
	if !iolib.FileExists(path) {
		return fmt.Errorf("profile not found: %s", name)
	}
	vp := viper.New()
	vp.SetConfigFile(path)
	if err := vp.ReadInConfig(); err != nil {
		return fmt.Errorf("cannot read profile %s: %w", path, err)
	}

	var problems []string
	for _, key := range vp.AllKeys() {
		if !knownProfileKeys[strings.ToLower(key)] {
			problems = append(problems, "unknown key: "+key)
		}
	}
	for _, key := range []string{"curateddomains", "linkok", "regexrankingkeywords"} {
		if strings.TrimSpace(vp.GetString(key)) == "" {
			problems = append(problems, "missing or empty: "+key)
		}
	}
	seeds := vp.GetStringSlice("bootstrapinglinks")
	if len(seeds) == 0 {
		problems = append(problems, "missing or empty: bootstrapingLinks")
	}
	for _, seed := range seeds {
		u, err := url.Parse(strings.TrimSpace(seed))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			problems = append(problems, "invalid seed URL: "+seed)
		}
	}
	for label, expr := range map[string]string{
		"curatedDomains":       vp.GetString("curateddomains"),
		"linkOk":               `(?i)^https*://.*(` + vp.GetString("linkok") + `)`,
		"regexRankingKeywords": vp.GetString("regexrankingkeywords"),
	} {
		if _, err := regexp.Compile(expr); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", label, err))
		}
	}
	if minDoc, maxDoc := vp.GetInt("mindoclen"), vp.GetInt("maxdoclen"); minDoc < 0 || maxDoc <= minDoc {
		problems = append(problems, fmt.Sprintf("invalid document length range %d..%d", minDoc, maxDoc))
	}
	minRatio, maxRatio := app.MinStopwordRatio, app.MaxStopwordRatio
	if vp.IsSet("minstopwordratio") {
		minRatio = vp.GetFloat64("minstopwordratio")
	}
	if vp.IsSet("maxstopwordratio") {
		maxRatio = vp.GetFloat64("maxstopwordratio")
	}
	if math.IsNaN(minRatio) || math.IsNaN(maxRatio) || minRatio < 0 || maxRatio > 1 || minRatio > maxRatio {
		problems = append(problems, fmt.Sprintf("invalid stopword ratio range %.3f..%.3f", minRatio, maxRatio))
	}

	fmt.Printf("Linting profile %s: %s\n", name, vp.ConfigFileUsed())
	if len(problems) == 0 {
		fmt.Println("OK: profile is valid")
		return nil
	}
	for _, problem := range problems {
		fmt.Println("  - " + problem)
	}
	return fmt.Errorf("profile %s has %d problem(s)", name, len(problems))
}

// lastExecutedProfile derives the most recent profile from session log filenames.
func lastExecutedProfile() string {
	files, err := filepath.Glob("logs/*-Z??-??-??.txt")
	if err != nil || len(files) == 0 {
		return ""
	}
	sort.Slice(files, func(i, j int) bool { return files[i] > files[j] })
	base := filepath.Base(files[0])
	parts := strings.Split(base, "-20")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// yamlBlockAlternatives extracts "|"-separated terms from a YAML literal block.
func yamlBlockAlternatives(text, key string) []string {
	i := strings.Index(text, key)
	if i < 0 {
		return nil
	}
	headerEnd := strings.Index(text[i:], "\n")
	if headerEnd < 0 {
		return nil
	}
	content := text[i+headerEnd+1:]
	if end := strings.Index(content, "\n"); end >= 0 {
		content = content[:end]
	}
	value := strings.TrimSpace(content)
	for _, prefix := range []string{`(?i)\W(`, `\W(`} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimPrefix(value, prefix)
			break
		}
	}
	value = strings.TrimSuffix(value, `)\W`)
	return listUnique(strings.Split(value, "|"))
}

// replaceYamlBlock replaces the content line of a YAML literal block.
func replaceYamlBlock(text, key, value string) string {
	i := strings.Index(text, key)
	if i < 0 {
		return text
	}
	headerEnd := strings.Index(text[i:], "\n")
	if headerEnd < 0 {
		return text + "\n  " + value + "\n"
	}
	contentStart := i + headerEnd + 1
	contentEnd := strings.Index(text[contentStart:], "\n")
	if contentEnd < 0 {
		contentEnd = len(text) - contentStart
	}
	return text[:contentStart] + "  " + value + text[contentStart+contentEnd:]
}

// keywordFamily returns the singular/plural/verb forms of a keyword.
func keywordFamily(word string) []string {
	w := strings.ToLower(strings.TrimSpace(word))
	if len(w) < 3 || guiStopwords[w] {
		return nil
	}
	forms := map[string]bool{w: true}
	add := func(value string) {
		if len(value) >= 3 && !guiStopwords[value] {
			forms[value] = true
		}
	}

	base := w
	if strings.HasSuffix(base, "ing") && len(base) > 5 {
		base = strings.TrimSuffix(base, "ing")
	} else if strings.HasSuffix(base, "ed") && len(base) > 4 {
		base = strings.TrimSuffix(base, "ed")
	} else if strings.HasSuffix(base, "es") && len(base) > 4 {
		base = strings.TrimSuffix(base, "es")
	} else if strings.HasSuffix(base, "s") && !strings.HasSuffix(base, "ss") && len(base) > 3 {
		base = strings.TrimSuffix(base, "s")
	}
	add(base)

	plural := base
	switch {
	case strings.HasSuffix(plural, "y") && len(plural) > 3:
		plural = plural[:len(plural)-1] + "ies"
	case strings.HasSuffix(plural, "s"), strings.HasSuffix(plural, "x"), strings.HasSuffix(plural, "ch"), strings.HasSuffix(plural, "sh"):
		plural += "es"
	default:
		plural += "s"
	}
	add(plural)

	verbBase := base
	if strings.HasSuffix(verbBase, "e") {
		add(verbBase[:len(verbBase)-1] + "ing")
		add(verbBase + "d")
	} else {
		add(verbBase + "ing")
		add(verbBase + "ed")
	}

	out := []string{}
	for form := range forms {
		out = append(out, form)
	}
	sort.Strings(out)
	return out
}

// expandKeywords adds morphological variants to a keyword list, removing stopwords.
func expandKeywords(words []string) []string {
	seen := make(map[string]bool)
	for _, word := range words {
		for _, variant := range keywordFamily(word) {
			seen[variant] = true
		}
	}
	out := []string{}
	for word := range seen {
		out = append(out, word)
	}
	sort.Strings(out)
	return out
}

var reMarkdownLink = regexp.MustCompile(`\[(https?://[^\]]+)\]\((https?://[^)]+)\)`)

// normalizeSeedURLs filters and trims a list of candidate seed URLs.
func normalizeSeedURLs(raw []string) []string {
	seen := make(map[string]bool)
	out := []string{}
	for _, value := range raw {
		value = strings.TrimSpace(strings.Trim(value, `"'`))
		if match := reMarkdownLink.FindStringSubmatch(value); match != nil {
			value = match[2]
		}
		value = strings.TrimRight(value, ".,;)")
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// profileSeedURLs reads the bootstrapingLinks list from a profile.
func profileSeedURLs(text string) []string {
	urls := []string{}
	inList := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "bootstrapingLinks:") {
			inList = true
			continue
		}
		if inList {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.HasPrefix(trimmed, "-") {
				urls = append(urls, strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
				continue
			}
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
				strings.Contains(trimmed, ":") {
				break
			}
		}
	}
	return normalizeSeedURLs(urls)
}

// replaceSeedBlock rewrites the bootstrapingLinks list of a profile, removing
// any old seed lines elsewhere in the document so duplicates cannot accumulate.
func replaceSeedBlock(text string, urls []string) string {
	lines := strings.Split(text, "\n")
	insertAt := -1
	cleaned := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "bootstrapingLinks:") {
			if insertAt < 0 {
				cleaned = append(cleaned, "bootstrapingLinks:")
				insertAt = len(cleaned)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && (strings.Contains(trimmed, "http://") || strings.Contains(trimmed, "https://")) {
			continue
		}
		cleaned = append(cleaned, line)
	}
	newLines := []string{}
	for _, u := range urls {
		newLines = append(newLines, `- "`+u+`"`)
	}
	if insertAt < 0 {
		return strings.TrimRight(strings.Join(cleaned, "\n"), "\n") + "\nbootstrapingLinks:\n" + strings.Join(newLines, "\n") + "\n"
	}
	result := append([]string{}, cleaned[:insertAt]...)
	result = append(result, newLines...)
	result = append(result, cleaned[insertAt:]...)
	return strings.Join(result, "\n")
}

var guiLockDirOverride string

// guiStateDir returns the per-user directory holding Workbench lock/registry files.
func guiStateDir() (string, error) {
	base := guiLockDirOverride
	var err error
	if base == "" {
		base, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(base, "goCrawler")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// guiLockPath returns the single-instance lock file.
func guiLockPath() (string, error) {
	dir, err := guiStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gui.lock"), nil
}

type guiInstance struct {
	PID  int
	Port string
	Path string
}

// guiRegistryDir holds one file per running Workbench instance.
func guiRegistryDir() (string, error) {
	dir, err := guiStateDir()
	if err != nil {
		return "", err
	}
	instances := filepath.Join(dir, "instances")
	if err := os.MkdirAll(instances, 0755); err != nil {
		return "", err
	}
	return instances, nil
}

// listGUIInstances returns other running Workbench instances and prunes stale files.
func listGUIInstances() ([]guiInstance, error) {
	dir, err := guiRegistryDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	instances := []guiInstance{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(content))
		pid := 0
		if len(fields) > 0 {
			pid, _ = strconv.Atoi(fields[0])
		}
		port := "unknown"
		if len(fields) > 1 {
			port = fields[1]
		}
		if pid == os.Getpid() {
			continue
		}
		if processAlive(pid) {
			instances = append(instances, guiInstance{PID: pid, Port: port, Path: path})
		} else {
			os.Remove(path)
		}
	}
	return instances, nil
}

// registerGUIInstance advertises this process and returns its unregister function.
func registerGUIInstance(port int) (func(), error) {
	dir, err := guiRegistryDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%d.pid", os.Getpid(), port))
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d %d\n", os.Getpid(), port)), 0644); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { os.Remove(path) }) }, nil
}

var stdinIsTerminal = func() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

var confirmTerminate = confirmTerminateInstances

// confirmTerminateInstances asks the user whether old instances should be stopped.
func confirmTerminateInstances(instances []guiInstance) bool {
	fmt.Println("Another goCrawler Workbench is already running:")
	for _, inst := range instances {
		fmt.Printf("  PID %d (port %s)\n", inst.PID, inst.Port)
	}
	fmt.Print("Terminate these instances and start a new one? [Y/n] ")
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && answer == "" {
		return false
	}
	return confirmAnswer(answer)
}

// confirmAnswer treats an empty answer as yes; only n/no decline.
func confirmAnswer(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes", "s", "si":
		return true
	default:
		return false
	}
}

var terminateGUIPID = terminateProcess

// terminateProcess sends SIGTERM (or Kill on Windows) to a PID.
func terminateProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return proc.Kill()
	}
	return proc.Signal(syscall.SIGTERM)
}

// prepareGUIInstances offers to close previously registered Workbench instances.
func prepareGUIInstances() error {
	instances, err := listGUIInstances()
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}

	details := make([]string, 0, len(instances))
	for _, inst := range instances {
		details = append(details, fmt.Sprintf("  PID %d (port %s)", inst.PID, inst.Port))
	}
	if !stdinIsTerminal() {
		return fmt.Errorf("another goCrawler Workbench is already running:\n%s\nstop it before starting a new one", strings.Join(details, "\n"))
	}
	if !confirmTerminate(instances) {
		return fmt.Errorf("cancelled by user")
	}

	for _, inst := range instances {
		if !processAlive(inst.PID) {
			os.Remove(inst.Path)
			continue
		}
		if err := terminateGUIPID(inst.PID); err != nil {
			return fmt.Errorf("cannot terminate PID %d: %w", inst.PID, err)
		}
	}
	for i := 0; i < 30; i++ {
		remaining, err := listGUIInstances()
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("previous Workbench instances did not stop in time")
}

// processAlive reports whether a PID still refers to a running process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM/EACCES means the process exists but belongs to another user.
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

// acquireGUILock creates the single-instance lock and returns its release function.
func acquireGUILock() (func(), error) {
	path, err := guiLockPath()
	if err != nil {
		return nil, fmt.Errorf("GUI lock: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(file, "%d %d\n", os.Getpid(), app.GUIPort)
			file.Close()
			var once sync.Once
			return func() { once.Do(func() { os.Remove(path) }) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("GUI lock: %w", err)
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("GUI lock: %w", readErr)
		}
		fields := strings.Fields(string(content))
		pid := 0
		if len(fields) > 0 {
			pid, _ = strconv.Atoi(fields[0])
		}
		port := "unknown"
		if len(fields) > 1 {
			port = fields[1]
		}
		if processAlive(pid) {
			return nil, fmt.Errorf("another goCrawler Workbench is already running (PID %d, port %s); stop it before starting a new one", pid, port)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("GUI lock: cannot remove stale lock: %w", err)
		}
	}
	return nil, fmt.Errorf("GUI lock: cannot acquire lock at %s after clearing stale file", path)
}

// runGUI starts a single embedded local web interface on loopback only.
func runGUI() error {
	if app.GUIPort <= 0 || app.GUIPort > 65535 {
		app.GUIPort = 8765
	}
	if err := prepareGUIInstances(); err != nil {
		return err
	}
	release, err := acquireGUILock()
	if err != nil {
		return err
	}
	defer release()
	unregister, err := registerGUIInstance(app.GUIPort)
	if err != nil {
		return err
	}
	defer unregister()

	addr := fmt.Sprintf("127.0.0.1:%d", app.GUIPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s (another Workbench may still be running): %w", addr, err)
	}
	fmt.Printf("goCrawler Workbench: http://%s\n", addr)

	server := &http.Server{Handler: guiHandler()}
	stop := make(chan os.Signal, 2)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

// safeProfileFilename keeps profile names inside output/ without path traversal.
func safeProfileFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "profile"
	}
	return b.String()
}

// profileCorpusMatchers builds the URL and keyword matchers for one profile.
func profileCorpusMatchers(profile string) (string, *regexp.Regexp, *regexp.Regexp, error) {
	resolved, err := resolveProfileName(profile)
	if err != nil {
		return "", nil, nil, err
	}
	vp := viper.New()
	vp.SetConfigFile("profiles/" + resolved + ".yaml")
	if err := vp.ReadInConfig(); err != nil {
		return "", nil, nil, err
	}

	linkExpr := `(?i)^https*://.*(` + stringlib.RmNewLines(vp.GetString("linkOk")) + `|` +
		stringlib.RmNewLines(vp.GetString("curatedDomains")) + `)`
	linkRe, err := regexp.Compile(linkExpr)
	if err != nil {
		return "", nil, nil, fmt.Errorf("invalid linkOk/curatedDomains: %w", err)
	}

	var keywordRe *regexp.Regexp
	if keywordExpr := stringlib.RmNewLines(vp.GetString("regexRankingKeywords")); keywordExpr != "" {
		keywordRe, err = regexp.Compile(keywordExpr)
		if err != nil {
			return "", nil, nil, fmt.Errorf("invalid regexRankingKeywords: %w", err)
		}
	}
	return resolved, linkRe, keywordRe, nil
}

// saveProfileCorpus exports curated documents matching a profile to a TXT file.
func saveProfileCorpus(profile, requestedScope string) (corpusExportInfo, error) {
	resolved, linkRe, keywordRe, err := profileCorpusMatchers(profile)
	if err != nil {
		return corpusExportInfo{}, err
	}
	if err := os.MkdirAll("output", 0755); err != nil {
		return corpusExportInfo{}, err
	}
	scope := requestedScope
	if scope == "" {
		scope = "all"
	}

	safe := safeProfileFilename(resolved) + "-" + scope
	relPath := filepath.Join("output", "corpus-"+safe+".txt")
	var b strings.Builder
	fmt.Fprintf(&b, "goCrawler corpus export\nProfile: %s\nScope: %s\nGenerated: %s\n\n",
		resolved, scope, time.Now().UTC().Format(time.RFC3339))

	documents := 0
	for _, doc := range filterCorpusScope(corpusDocuments(1<<30), scope) {
		docURL, text := doc["url"], doc["text"]
		if !linkRe.MatchString(docURL) {
			continue
		}
		if keywordRe != nil && !keywordRe.MatchString(text) {
			continue
		}
		documents++
		fmt.Fprintf(&b, "================================================================================\nURL: %s\n--------------------------------------------------------------------------------\n%s\n\n", docURL, text)
	}
	content := b.String()
	if err := os.WriteFile(relPath, []byte(content), 0644); err != nil {
		return corpusExportInfo{}, err
	}
	return corpusExportInfo{
		File:       relPath,
		Download:   "/api/corpus/download?profile=" + url.QueryEscape(resolved) + "&scope=" + url.QueryEscape(scope),
		Documents:  documents,
		Characters: len(content),
		Profile:    resolved,
		Scope:      scope,
	}, nil
}

// markCorpusSession writes a session boundary into the curated corpus log.
func markCorpusSession() {
	path := "./logs/corpusCuratedText.log"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "@@SESSION %s\n", time.Now().UTC().Format(time.RFC3339))
}

// corpusDocuments reads accepted documents from the curated corpus.
func corpusDocuments(limit int) []map[string]string {
	result := []map[string]string{}
	path := "./logs/corpusCuratedText.log"
	if !iolib.FileExists(path) {
		return result
	}
	var url, text, session string
	flush := func() {
		if url != "" && len(result) < limit {
			result = append(result, map[string]string{
				"url": url, "text": strings.TrimSpace(text), "session": session,
			})
		}
	}
	for _, line := range strings.Split(iolib.File2string(path), "\n") {
		if strings.HasPrefix(line, "@@SESSION ") {
			flush()
			url, text = "", ""
			session = strings.TrimSpace(strings.TrimPrefix(line, "@@SESSION "))
		} else if strings.HasPrefix(line, "http") {
			flush()
			url, text = strings.TrimSpace(line), ""
		} else if line != "----" {
			text += line + "\n"
		}
	}
	flush()
	return result
}

// filterCorpusScope keeps documents from all runs, the last run or today.
func filterCorpusScope(docs []map[string]string, scope string) []map[string]string {
	if scope == "" {
		scope = "all"
	}
	if scope == "all" {
		return docs
	}

	filtered := []map[string]string{}
	if scope == "last" {
		last := ""
		for _, doc := range docs {
			if value := doc["session"]; value != "" {
				last = value
			}
		}
		if last == "" {
			return filtered
		}
		for _, doc := range docs {
			if doc["session"] == last {
				filtered = append(filtered, doc)
			}
		}
		return filtered
	}

	if scope == "today" {
		today := time.Now().UTC().Format("2006-01-02")
		for _, doc := range docs {
			if strings.HasPrefix(doc["session"], today) {
				filtered = append(filtered, doc)
			}
		}
	}
	return filtered
}

// usage prints the command-line usage message.
func usage() {
	fmt.Println("Usage: ./run.sh <profile>   (or: go run crawler.go <profile> from the module root)")
	fmt.Println("  <profile>  profile name (without .yaml), from profiles/ or the working directory")
	fmt.Println("  --selftest run built-in checks and exit")
	fmt.Println("  --resume   restore a previous crawl from the cache store")
	fmt.Println("  -profile=, -backend=, -cache=, -log-level=, -workers=N, -max-links=N")
	fmt.Println("  -dry-run, -explore, -query=TEXT, -generate-profile=URL, -version, -license, -gui [-gui-port=N]")
	fmt.Println("  Profiles accept minStopwordRatio / maxStopwordRatio (defaults 0.1 / 0.38).")
	fmt.Println("  -min-stopword-ratio=F, -max-stopword-ratio=F, -lint-profile=NAME")
	fmt.Println("  -reject-report=TRACE.jsonl, -trace-url=SUBSTRING [-trace-file=TRACE.jsonl]")
	fmt.Println("Examples: ./run.sh crypto --resume | ./run.sh -lint-profile=technical | ./run.sh -query=drizzle")
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
	if app.CLIProfileLint != "" {
		return true, lintProfile(app.CLIProfileLint)
	}
	if app.CLIRejectReport != "" {
		return true, rejectReportFile(app.CLIRejectReport)
	}
	if app.CLITraceURL != "" {
		tracePath := app.RejectTracePath
		if app.CLITraceFile != "" {
			tracePath = app.CLITraceFile
		}
		return true, traceRejectedURL(tracePath, app.CLITraceURL)
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
	if math.IsNaN(app.MinStopwordRatio) || math.IsNaN(app.MaxStopwordRatio) ||
		app.MinStopwordRatio < 0 || app.MaxStopwordRatio > 1 ||
		app.MinStopwordRatio > app.MaxStopwordRatio {
		problems = append(problems, fmt.Sprintf("invalid stopword ratio range %.3f..%.3f", app.MinStopwordRatio, app.MaxStopwordRatio))
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
	if err := rotateSessionLogs(); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: session log rotation failed:", err)
	}
	if err := openRejectTrace(); err != nil {
		return 0, noop, err
	}
	if err := os.MkdirAll("./output", os.ModePerm); err != nil {
		return 0, noop, err
	}
	markCorpusSession()

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
		closeRejectTrace()
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
		robotsPath := filepath.Join(filepath.Dir(app.CachePath), "robots-cache.json")
		app.RobotsPolicy = robotspolicy.NewWithCache(app.UserAgent, app.DownloadTimeout, robotsPath)
	}
	if err := corpusfreqlib.Init(); err != nil {
		return 0, cleanup, fmt.Errorf("English corpus: %w", err)
	}

	app.CrawlStartedAt = time.Now()
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
	var nextIndex atomic.Int64
	nextIndex.Store(int64(startIndex))
	var wg sync.WaitGroup
	for i := 0; i < app.CLIWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ctx := guiCrawlContext()
				if app.StopRequested.Load() || (ctx != nil && ctx.Err() != nil) {
					return
				}
				if !app.CrawlStartedAt.IsZero() && time.Since(app.CrawlStartedAt) >= maxCrawlDuration {
					app.StopRequested.Store(true)
					return
				}
				index := int(nextIndex.Add(1) - 1)
				app.CrawlMu.Lock()
				app.LastProcessedLink = index
				app.CrawlMu.Unlock()
				if !doNextLink(index) {
					return
				}
				pruneTerminalLinks()
			}
		}()
	}
	wg.Wait()
	app.CrawlMu.Lock()
	defer app.CrawlMu.Unlock()
	return app.LastProcessedLink
}

// reportResults writes the final summaries.
var sessionLogPattern = regexp.MustCompile(`-Z\d\d-\d\d-\d\d\.txt$`)

// gzipSessionLog compresses a completed session log using a temporary file so
// an interrupted rotation never leaves a half-written .gz behind.
func gzipSessionLog(path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := path + ".gz.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path+".gz"); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(path)
}

// rotateSessionLogs gzips session logs older than LogRotateDays or when their
// combined size exceeds LogRotateMaxMB. Setting both to 0 disables rotation.
// Active session files, corpusCuratedText.log and the dedup logs are untouched.
func rotateSessionLogs() error {
	if app.LogRotateDays <= 0 && app.LogRotateMaxMB <= 0 {
		return nil
	}
	entries, err := os.ReadDir("./logs")
	if err != nil {
		return err
	}
	type item struct {
		path string
		info os.FileInfo
	}
	var items []item
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !sessionLogPattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, item{filepath.Join("./logs", entry.Name()), info})
		total += info.Size()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].info.ModTime().Before(items[j].info.ModTime()) })
	limit := int64(app.LogRotateMaxMB) << 20
	for _, it := range items {
		old := app.LogRotateDays > 0 && time.Since(it.info.ModTime()) > time.Duration(app.LogRotateDays)*24*time.Hour
		large := app.LogRotateMaxMB > 0 && total > limit
		if !old && !large {
			continue
		}
		if err := gzipSessionLog(it.path); err != nil {
			return err
		}
		total -= it.info.Size()
	}
	return nil
}

const maxRejectTraceBytes = 20 << 20

// openRejectTrace opens the append-only JSONL sink for rejection events,
// compressing it first if it has grown too large.
func openRejectTrace() error {
	if app.RejectTracePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(app.RejectTracePath), os.ModePerm); err != nil {
		return err
	}
	if info, err := os.Stat(app.RejectTracePath); err == nil && info.Size() > maxRejectTraceBytes {
		if err := gzipSessionLog(app.RejectTracePath); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(app.RejectTracePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	app.rejectTraceFile = file
	return nil
}

// closeRejectTrace closes the JSONL sink; it is safe to call twice.
func closeRejectTrace() {
	if app.rejectTraceFile != nil {
		app.rejectTraceFile.Close()
		app.rejectTraceFile = nil
	}
}

// readRejectTrace loads a JSONL trace, skipping malformed lines.
func readRejectTrace(path string) ([]rejectionEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []rejectionEvent
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event rejectionEvent
		if err := json.Unmarshal([]byte(line), &event); err == nil {
			events = append(events, event)
		}
	}
	return events, nil
}

// writeRejectionReport writes a breakdown by reason with up to three sampled
// paragraphs per reason.
func writeRejectionReport(events []rejectionEvent, path string) error {
	counts, samples := map[string]int{}, map[string][]string{}
	for _, event := range events {
		counts[event.Reason]++
		if event.Text == "" || len(samples[event.Reason]) >= 3 {
			continue
		}
		snippet := event.Text
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		samples[event.Reason] = append(samples[event.Reason],
			fmt.Sprintf("%s | %s | %s", event.URL, event.Detail, snippet))
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	var out strings.Builder
	out.WriteString("# Rejection report\n\n")
	for _, reason := range reasons {
		fmt.Fprintf(&out, "## %s: %d\n", reason, counts[reason])
		for _, sample := range samples[reason] {
			fmt.Fprintf(&out, "- %s\n", sample)
		}
		out.WriteString("\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out.String()), 0644)
}

// rejectReportFile summarises a JSONL trace into logs/rejection-report.txt.
func rejectReportFile(path string) error {
	events, err := readRejectTrace(path)
	if err != nil {
		return err
	}
	if err := writeRejectionReport(events, "./logs/rejection-report.txt"); err != nil {
		return err
	}
	fmt.Printf("Rejection report: ./logs/rejection-report.txt (%d events)\n", len(events))
	return nil
}

// traceRejectedURL prints every rejection recorded for URLs containing filter.
func traceRejectedURL(path, filter string) error {
	events, err := readRejectTrace(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no rejection trace at %s; run a crawl first or use -reject-report=FILE", path)
		}
		return err
	}
	found := 0
	for _, event := range events {
		if !strings.Contains(event.URL, filter) {
			continue
		}
		found++
		fmt.Printf("%s reason=%s profile=%s seed=%s\n  detail=%s\n",
			event.Time.Format(time.RFC3339), event.Reason, event.Profile, event.Seed, event.Detail)
		if event.Text != "" {
			fmt.Printf("  text=%s\n", event.Text)
		}
	}
	if found == 0 {
		return fmt.Errorf("no rejection events for %q in %s", filter, path)
	}
	return nil
}

func reportResults(finalIndex int) {
	dumpFrequencies(finalIndex, true)
	app.Logger.Info("crawl finished", "processed", finalIndex,
		"accepted", app.AcceptedDocs, "rejections", rejectionSummary())
	closeRejectTrace()
	if app.RejectTracePath != "" {
		if events, err := readRejectTrace(app.RejectTracePath); err == nil {
			if err := writeRejectionReport(events, "./logs/rejection-report.txt"); err == nil {
				fmt.Println("Rejection report: ./logs/rejection-report.txt")
			}
		}
	}
	notifyCompletion(finalIndex)
	fmt.Println("\n\n\n***** Done!!!")
}

// ensureModuleRoot moves to the module root when the working directory lacks
// crawler.yaml but an ancestor (or the executable's directory chain) has it.
// This lets a compiled binary run from any directory. `go run crawler.go` must
// still be launched from the module root because Go resolves goCrawler/...
// imports from the working directory; ./run.sh does that automatically.
func ensureModuleRoot() {
	if iolib.FileExists("crawler.yaml") {
		return
	}
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-config=") || strings.HasPrefix(arg, "--config=") {
			return // explicit config: respect the caller's paths
		}
	}
	starts := make([]string, 0, 3)
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	// Source path baked in at build time lets a compiled binary find its tree
	// even when installed outside it.
	if _, file, _, ok := runtime.Caller(0); ok {
		starts = append(starts, filepath.Dir(file))
	}
	for _, start := range starts {
		dir := start
		for {
			if iolib.FileExists(filepath.Join(dir, "go.mod")) && iolib.FileExists(filepath.Join(dir, "crawler.yaml")) {
				if err := os.Chdir(dir); err == nil {
					fmt.Printf("Module root detected: %s\n", dir)
					return
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
}

// run initializes the crawler, executes the crawl and reports results.
func run() error {
	app = newCrawler()
	ensureModuleRoot()
	crawlCtx, cancelCrawl = context.WithCancel(context.Background())
	defer cancelCrawl()
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupt received, saving state...")
		app.StopRequested.Store(true)
		app.StateDirty = true
		cancelCrawl()
		<-sigCh
		fmt.Println("\nSecond interrupt: forced exit")
		os.Exit(130)
	}()
	handled, err := prepareCLI()
	if err != nil {
		return err
	}
	if handled {
		return nil
	}

	startIndex, cleanup, err := setupRuntime(app.ProfileName)
	if err != nil {
		notifyError(err.Error())
		return err
	}
	defer cleanup()

	if app.CLIDryRun {
		fmt.Printf("Dry run: profile=%s backend=%s cache=%s language=%s strategy=%s workers=%d stopwordRatio=%.2f..%.2f\n",
			app.ProfileName, app.CacheBackend, app.CachePath, app.TargetLanguage, app.RankingStrategy,
			app.CLIWorkers, app.MinStopwordRatio, app.MaxStopwordRatio)
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
	app.MinStopwordRatio = 0.1
	app.MaxStopwordRatio = 0.38
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
