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
	"io"
	"io/ioutil"
	"log"
	"math"
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

/******************************************************************************/
/******************************************************************************/
/*********************** CONFIG ***********************************************/
/******************************************************************************/
/******************************************************************************/

var regexBannedDomains, regexLinkBannedTokens, curatedDomains, regexLinkOk string
var engStopWordsWOthe, engStopWords, engLowRelevancyWords, regexStopwords string
var regexRankingKeywords, proxyHost, proxyUser, proxyPass string
var downloadTimeout time.Duration
var maxDownloadBytes int64 = 5 << 20
var bootstrapingLinks []string
var minDocLen, maxDocLen int
var maxLinksPerPage int
var crawlDelay time.Duration
var domainLastReq = make(map[string]time.Time)
var domainDelayMu sync.Mutex
var rankingStrategy = "hybrid"
var synonyms = make(map[string][]string)
var reRankingExtended *regexp.Regexp
var corpusDocFreq = make(map[string]int)
var corpusDocCount int
var scoreThreshold float64
var addUrl string
var cacheBackend, cachePath string
var cacheStore storelib.Store
var robotsPolicy *robotspolicy.Policy
var userAgent string
var respectRobots bool
var logLevel string
var httpClient = &http.Client{Timeout: 10 * time.Second}

// initHTTPClient builds the shared HTTP client with connection reuse.
func initHTTPClient() {
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   downloadTimeout,
		ResponseHeaderTimeout: downloadTimeout,
	}
	if proxyHost != "" {
		transport.Proxy = http.ProxyURL(&url.URL{
			Scheme: "http",
			User:   url.UserPassword(proxyUser, proxyPass),
			Host:   proxyHost,
		})
	}
	httpClient = &http.Client{Transport: transport, Timeout: downloadTimeout}
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
	reBannedDomains = regexp.MustCompile(regexBannedDomains)
	reBannedTokens = regexp.MustCompile(regexLinkBannedTokens)
	reStopwords = regexp.MustCompile(regexStopwords)
	reStopWords = regexp.MustCompile(`(?i)\W(` + engStopWords + `)\W`)
	reStopLeft = regexp.MustCompile(`(?i)^(` + engStopWords + `)\W`)
	reStopRight = regexp.MustCompile(`(?i)\W(` + engStopWords + `)$`)
	reLowRelLeft = regexp.MustCompile(`(?i)^(` + engStopWords + engLowRelevancyWords + `)\W`)
	reLinkOk = regexp.MustCompile(regexLinkOk)
	reRankingKw = regexp.MustCompile(regexRankingKeywords)
}

// logger is replaced at startup so records are written through the session tee.
var logger = slog.New(slog.NewTextHandler(os.Stdout, nil))

var rejectionCounts = make(map[string]int)
var acceptedDocs int
var lastProcessedLink int
var resumeEnabled bool

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
	poolMu.Lock()
	failMu.Lock()
	crawlMu.Lock()
	defer crawlMu.Unlock()
	defer failMu.Unlock()
	defer poolMu.Unlock()
	state := crawlState{
		Version:       crawlStateVersion,
		Pool:          lPool,
		DomainCounter: domainCounter,
		FailedDomains: failedDomains,
		Processed:     lastProcessedLink,
		UniqueSigs:    uniqueSignature,
		Frequencies:   corpusFreqs,
		DocFreq:       corpusDocFreq,
		DocCount:      corpusDocCount,
		SavedAt:       time.Now(),
	}
	b, err := json.Marshal(state)
	if err != nil {
		logger.Error("cannot serialize crawl state", "error", err)
		return
	}
	if err := cacheStore.Set(crawlStateKey, string(b)); err != nil {
		logger.Error("cannot save crawl state", "error", err)
		return
	}
	logger.Debug("crawl state saved", "processed", lastProcessedLink)
}

// loadCrawlState restores a previous crawl and returns the next link index.
func loadCrawlState() int {
	value := cacheStore.Get(crawlStateKey)
	if value == "" {
		logger.Warn("no crawl state found, starting from scratch")
		return 0
	}
	var state crawlState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		logger.Error("cannot decode crawl state", "error", err)
		return 0
	}
	if state.Version != crawlStateVersion {
		logger.Warn("unsupported crawl state version", "version", state.Version, "expected", crawlStateVersion)
		return 0
	}
	lPool = state.Pool
	linkIndex = make(map[string]int, len(lPool))
	for i, link := range lPool {
		linkIndex[link.URL] = i
	}
	domainCounter = state.DomainCounter
	if state.FailedDomains != nil {
		failedDomains = state.FailedDomains
	}
	uniqueSignature = state.UniqueSigs
	corpusFreqs = state.Frequencies
	if state.DocFreq != nil {
		corpusDocFreq = state.DocFreq
	}
	corpusDocCount = state.DocCount
	lastProcessedLink = state.Processed
	logger.Info("crawl state restored",
		"processed", state.Processed,
		"links", len(lPool),
		"savedAt", state.SavedAt)
	return state.Processed
}

// initLogger configures the structured logger level.
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
	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
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

// buildRankingRegex merges profile keywords and synonyms into one matcher.
func buildRankingRegex() {
	terms := regexTermList(regexRankingKeywords)
	for _, alts := range synonyms {
		terms = append(terms, alts...)
	}
	terms = listUnique(terms)
	reRankingExtended = regexp.MustCompile(`(?i)\W(` + strings.Join(terms, "|") + `)\W`)
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
	if crawlDelay <= 0 {
		return
	}
	domain := getDomain(rawURL)
	domainDelayMu.Lock()
	now := time.Now()
	next := now
	if last, ok := domainLastReq[domain]; ok && last.After(now) {
		next = last
	}
	domainLastReq[domain] = next.Add(crawlDelay)
	domainDelayMu.Unlock()
	if sleep := time.Until(next); sleep > 0 {
		time.Sleep(sleep)
	}
}

// countRejection increments the counter for one rejection reason.
func countRejection(reason string) {
	crawlMu.Lock()
	rejectionCounts[reason]++
	crawlMu.Unlock()
}

// rejectionSummary returns the rejection counters as log attributes.
func rejectionSummary() []interface{} {
	crawlMu.Lock()
	defer crawlMu.Unlock()
	keys := make([]string, 0, len(rejectionCounts))
	for k := range rejectionCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attrs := make([]interface{}, 0, len(keys)*2)
	for _, k := range keys {
		attrs = append(attrs, k, rejectionCounts[k])
	}
	return attrs
}

/***************************************************************************************************************
****************************************************************************************************************
* CSV FUNCTIONS ************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

var csvWriter *csv.Writer
var csvFile *os.File

// csvInit creates the ranking CSV writer, appending when resuming.
func csvInit() error {
	var file *os.File
	var err error
	if resumeEnabled {
		file, err = os.OpenFile("output/ranking-URL.csv", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	} else {
		file, err = os.Create("output/ranking-URL.csv")
	}
	if err != nil {
		return fmt.Errorf("cannot create ranking CSV: %w", err)
	}
	csvFile = file
	csvWriter = csv.NewWriter(file)
	csvWriter.Comma = '\t'
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

var lPool []aLink
var linkIndex = make(map[string]int)

type CachedData struct {
	Content string
	Links   []string
}

var domainCounter = make(map[string]int)
var failedDomains = make(map[string]int)

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
	resp, err = httpClient.Do(req)
	if printLog {
		fmt.Printf("\nEnd downloading")
	}
	return
}

// download fetches a page and returns its plain text and extracted links.
func download(urlLink string) (string, []string, error) {
	logDownload.Printf("\n%s", urlLink)
	domain := getDomain(urlLink)
	if domainHadFailed(domain) {
		logDownload.Printf("\tPreviously failed")
		return "", nil, errors.New("download: Previously failed")
	}
	logDownload.Printf("\tRequested")

	resp, err := proxyGet(urlLink, true)
	if err != nil {
		logDownload.Printf("\tHttp transport error:\n\t\t%s", err)
		domainReportFailed(domain)
		return "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logDownload.Printf("\tHTTP status: %d", resp.StatusCode)
		if resp.StatusCode >= 500 {
			domainReportFailed(domain)
		}
		resp.Body.Close()
		return "", nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" &&
		!strings.Contains(ct, "html") && !strings.Contains(ct, "text") && !strings.Contains(ct, "xml") {
		logDownload.Printf("\tUnsupported content type: %s", ct)
		resp.Body.Close()
		return "", nil, fmt.Errorf("download: unsupported content type %s", ct)
	}

	// Read the response body.
	bodyBytes, err := ioutil.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		logDownload.Printf("\t%s%s", "Read error: ", err)
		return "", nil, err
	}
	resp.Body.Close() // must close
	resp.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))

	// Extract links from the HTML body.
	links := collectlinks.All(resp.Body) // Here we use the collectlinks package

	// Convert the HTML body to plain text.
	plain, err := html2text.FromString(string(bodyBytes), html2text.Options{PrettyTables: false})
	if err != nil {
		logDownload.Printf("\t%s%s", "html2text.FromString fails: ", err)
		return "", nil, err
	}

	logDownload.Printf("\t%s len(plain): %d len(links): %d", "Ok: ", len(plain), len(links))

	return plain, links, nil
}

// downloadCached returns a cached page or downloads and stores a new one.
func downloadCached(urlLink string) (content string, links []string, err error) {
	var cached CachedData
	cacheKey := "cache_" + urlLink

	if cacheStore.Exists(cacheKey) {
		value := cacheStore.Get(cacheKey)
		if value != "" {
			if err := json.Unmarshal([]byte(value), &cached); err != nil {
				logger.Warn("discarding corrupt cache entry", "key", cacheKey, "error", err)
			} else {
				fmt.Printf(" [CACHE]")
				return cached.Content, cached.Links, nil
			}
		}
	}

	if robotsPolicy != nil {
		if !robotsPolicy.Allowed(urlLink) {
			countRejection("robots_blocked")
			logRejectedLink("robotsBlocked.log", urlLink)
			fmt.Printf(" [ROBOTS-BLOCKED]")
			return "", nil, errors.New("download: blocked by robots.txt")
		}
		robotsPolicy.Wait(urlLink)
	}
	respectCrawlDelay(urlLink)

	cached.Content, cached.Links, err = download(urlLink)
	if err != nil {
		return "", nil, err
	}
	encoded, marshalErr := json.Marshal(cached)
	if marshalErr != nil {
		logger.Error("cannot marshal cache entry", "error", marshalErr)
	} else if setErr := cacheStore.Set(cacheKey, string(encoded)); setErr != nil {
		logger.Error("cannot write cache entry", "error", setErr)
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
	poolMu.Lock()
	domainCounter[domain]++
	poolMu.Unlock()
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

const maxUniqueRejectedLinks = 1000000

var (
	poolMu   sync.Mutex
	rejectMu sync.Mutex
	failMu   sync.Mutex
	crawlMu  sync.Mutex
)

var rejectedLinkSets = map[string]map[string]struct{}{
	"linksNotOk.log":                 {},
	"linksNotOk.additional.log":      {},
	"linksNotOk.ParseRequestURI.log": {},
	"robotsBlocked.log":              {},
}

// initRejectedLinkSets loads existing rejected links to deduplicate across runs.
func initRejectedLinkSets() {
	rejectMu.Lock()
	defer rejectMu.Unlock()
	for filename, set := range rejectedLinkSets {
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
	rejectMu.Lock()
	defer rejectMu.Unlock()
	link = strings.TrimSpace(link)
	set, ok := rejectedLinkSets[filename]
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
			logger.Debug("link matched profile whitelist", "token", match[1], "url", l)
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
	poolMu.Lock()
	defer poolMu.Unlock()
	maxi := 0
	lasti := 0
	maxURL := ""
	var priority, maxPriority float64

	if len(lPool) == 0 {
		return maxi, ""
	}

	for i, l := range lPool {
		if l.Status == 4 { // bootstrapping url
			fmt.Printf("\n\nFound bootstrapping url: %+v", l)
			// Set this item directly as next candidate
			maxi = i
			maxURL = l.URL
			maxPriority = 0
			break
		}

		priority = float64(l.Count) * float64(l.Count) / (float64(domainCounter[l.Domain]) + 1.0)

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
	lPool[maxi].Status = 1
	domainCounter[lPool[maxi].Domain]++

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
			logCGI.Printf("\n\n%s\n%s", link2, link)
			link = link2
		}
	}

	// Look for an existing entry in the link pool using the URL index.
	if i, ok := linkIndex[link]; ok {
		lPool[i].Count++
		return true
	}

	// Register a new link in the pool.
	status := 0
	if avoidFilters {
		status = 4
	}
	linkIndex[link] = len(lPool)
	lPool = append(lPool, aLink{URL: link, Domain: domain, Count: 1, Status: status})

	return true
}

// linkBootstraping seeds the pool with the profile bootstrap URLs.
func linkBootstraping() {
	for _, l := range bootstrapingLinks {
		addLink(l, true)
	}

	goDebug.Print("linkBootstraping", lPool)
}

// lPoolDump writes the link pool to a JSON file.
func lPoolDump() {
	jdata, err := json.MarshalIndent(lPool, "", " ")
	if err != nil {
		fmt.Println("error: ", err)
	}
	jsonFile, err := os.Create("./output/LPool.json")
	if err != nil {
		log.Println(err)
	}
	jsonFile.Write(jdata)
}

// domainCounterDump writes the per-domain visit counters to a JSON file.
func domainCounterDump() {
	jdata, err := json.MarshalIndent(domainCounter, "", " ")
	if err != nil {
		fmt.Println("domainCounterDump error: ", err)
	}
	jsonFile, err := os.Create("./output/domainCounter.json")
	jsonFile.Write(jdata)
}

// domainReportFailed records a transport failure in memory and in the text log.
func domainReportFailed(domain string) {
	failMu.Lock()
	failedDomains[domain]++
	failMu.Unlock()
	iolib.String2fileAppend(domain+"\n", "./logs/domainFailed.log")
}

// domainHadFailed reports whether a domain exceeded the failure limit.
func domainHadFailed(domain string) bool {
	failMu.Lock()
	defer failMu.Unlock()
	return failedDomains[domain] > 6
}

// initFailedDomains rebuilds the in-memory counter from the text log.
func initFailedDomains() {
	failMu.Lock()
	defer failMu.Unlock()
	failedDomains = make(map[string]int)
	path := "./logs/domainFailed.log"
	if !iolib.FileExists(path) {
		return
	}
	for _, line := range strings.Split(iolib.File2string(path), "\n") {
		domain := strings.TrimSpace(line)
		if domain != "" {
			failedDomains[domain]++
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

// rankingScore scores a document using the configured strategy.
func rankingScore(text string) float64 {
	crawlMu.Lock()
	defer crawlMu.Unlock()
	switch rankingStrategy {
	case "keywords":
		return rankingByKeywords(text)
	case "tfidf":
		return tfidfScore(text)
	default:
		kw := 100.0 * float64(uniqueMatches(text, reRankingExtended)) / math.Sqrt(float64(1+len(tokenize(text))))
		return (kw + tfidfScore(text)) / 2
	}
}

// uniqueMatches counts distinct matched terms (including synonyms) in a text.
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
	if len(tokens) == 0 || reRankingExtended == nil {
		return 0
	}
	idf := func(term string) float64 {
		return math.Log(1 + float64(corpusDocCount+1)/float64(1+corpusDocFreq[strings.ToLower(term)]))
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
	for _, m := range reRankingExtended.FindAllStringSubmatch(text, -1) {
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

var counter int

// add increments the frequency of a token.
func (f freq) add(t string) {
	counter++
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
var (
	outfile, outfile3, outfile4 *os.File
	logDownload                 = log.New(io.Discard, "", 0)
	logCGI                      = log.New(io.Discard, "", 0)
	logDomainFailed             = log.New(io.Discard, "", 0)
)

// initFileLoggers opens the log files. It must run after main creates ./logs.
func initFileLoggers() error {
	var err error
	if outfile, err = os.Create("./logs/download.log"); err != nil {
		return fmt.Errorf("download log: %w", err)
	}
	logDownload = log.New(outfile, "", 0)
	if outfile3, err = os.Create("./logs/cgi.log"); err != nil {
		return fmt.Errorf("cgi log: %w", err)
	}
	logCGI = log.New(outfile3, "", 0)
	if outfile4, err = os.Create("./logs/domainFailed.log"); err != nil {
		return fmt.Errorf("domainFailed log: %w", err)
	}
	logDomainFailed = log.New(outfile4, "", 0)
	initFailedDomains()
	return nil
}

// closeFileLoggers flushes and closes the log files.
func closeFileLoggers() {
	for _, f := range []*os.File{outfile, outfile3, outfile4} {
		if f != nil {
			f.Close()
		}
	}
}

var uniqueSignature = make(map[string]string)
var corpusFreqs freq = make(freq)

// addLinksOf adds the links found in a page to the pool.
func addLinksOf(nextLink string, links []string) {
	linksAdded := 0
	limit := maxLinksPerPage
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
	if documentLanguage != "" && documentLanguage != targetLanguage {
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
	crawlMu.Lock()
	defer crawlMu.Unlock()
	if uniqueSignature[signature] == "" {
		uniqueSignature[signature] = url
		return true
	}
	countRejection("duplicate")
	logger.Debug("document rejected", "reason", "duplicate", "url", url, "duplicate_of", uniqueSignature[signature])
	return false
}

// writeDocument appends the curated text and its ranking row to the outputs.
func writeDocument(url, content, signature, bestParagraph string, score float64) error {
	crawlMu.Lock()
	defer crawlMu.Unlock()
	iolib.String2fileAppend(url+"\n"+content+"----\n\n\n\n", "./logs/corpusCuratedText.log")
	if err := csvWriter.Write([]string{fmt.Sprintf("%.02f", score), fmt.Sprintf("%d", len(tokenize(content))), signature, url, bestParagraph}); err != nil {
		return err
	}
	csvWriter.Flush()
	return nil
}

// pruneTerminalLinks removes finished links from the pool while keeping counters.
func pruneTerminalLinks() {
	poolMu.Lock()
	defer poolMu.Unlock()
	kept := make([]aLink, 0, len(lPool))
	for _, link := range lPool {
		if link.Status == 2 || link.Status == 3 {
			continue
		}
		kept = append(kept, link)
	}
	if len(kept) == len(lPool) {
		return
	}
	lPool = kept
	linkIndex = make(map[string]int, len(lPool))
	for i, link := range lPool {
		linkIndex[link.URL] = i
	}
}

// capAuxiliarySets bounds memory used by deduplication structures.
func capAuxiliarySets() {
	const cap = 100000
	crawlMu.Lock()
	if len(uniqueSignature) > cap {
		uniqueSignature = make(map[string]string)
	}
	crawlMu.Unlock()
	rejectMu.Lock()
	for _, set := range rejectedLinkSets {
		if len(set) > cap {
			for key := range set {
				delete(set, key)
				if len(set) <= cap/2 {
					break
				}
			}
		}
	}
	rejectMu.Unlock()
}

// dumpFrequencies writes frequency files and JSON dumps every 50 links, or when forced.
func dumpFrequencies(numLinksProcessed int, force bool) {
	if !force && numLinksProcessed%50 != 0 {
		return
	}
	pruneTerminalLinks()
	capAuxiliarySets()
	saveCrawlState()
	logger.Info("progress", "processed", numLinksProcessed, "accepted", acceptedDocs,
		"rejections", rejectionSummary())

	crawlMu.Lock()
	freqCopy := make(freq, len(corpusFreqs))
	for k, v := range corpusFreqs {
		freqCopy[k] = v
	}
	crawlMu.Unlock()
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
	if len(addUrl) == 0 {
		return
	}
	target := addUrl + url.QueryEscape(rawURL)
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

	poolMu.Lock()
	prevState := lPool[maxi].Status
	poolMu.Unlock()
	if stopRequested.Load() {
		return false
	}
	fmt.Printf("\n\n* Downloading url: %s", nextLink)

	content, links, err := downloadCached(nextLink)
	if err != nil {
		lPool[maxi].Status = 3
		fmt.Println("\nDownload error: ", strings.Trim(err.Error(), "\n"))
	} else {
		lPool[maxi].Status = 2
	}

	if prevState == 4 && lPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}

	paragraphs := filterParagraphs(splitParagraphs(content))
	best := bestParagraph(paragraphs)
	curated := buildCuratedContent(paragraphs)
	docLen := len(tokenize(curated))

	if docLen > maxDocLen {
		countRejection("too_long")
		logger.Debug("document rejected", "reason", "too_long", "url", nextLink, "tokens", docLen)
		return true
	}
	if docLen < minDocLen {
		countRejection("too_short")
		logger.Debug("document rejected", "reason", "too_short", "url", nextLink, "tokens", docLen)
		return true
	}

	signature, sortedFreqs := documentSignature(curated)
	if !isUniqueDocument(signature, nextLink) {
		return true
	}

	score := rankingScore(curated)
	if score < scoreThreshold {
		countRejection("low_relevancy")
		logger.Debug("document rejected", "reason", "low_relevancy", "url", nextLink, "score", score)
		return true
	}

	if len(sortedFreqs) > 0 {
		ratio := float64(sortedFreqs[0].Value) / float64(1+docLen)
		if ratio > 0.1 || ratio < 0.002 {
			countRejection("token_distribution")
			logger.Debug("document rejected", "reason", "token_distribution", "url", nextLink)
			return true
		}
	}

	if err := writeDocument(nextLink, curated, signature, best, score); err != nil {
		countRejection("output_error")
		logger.Error("cannot write document", "url", nextLink, "error", err)
		return true
	}
	indexDocument(nextLink, curated)
	crawlMu.Lock()
	acceptedDocs++
	corpusDocCount++
	for _, token := range tokenize(curated) {
		corpusDocFreq[strings.ToLower(token)]++
	}
	corpusFreqs.add(curated)
	crawlMu.Unlock()

	dumpFrequencies(numLinksProcessed, false)
	notifyIndexer(nextLink)

	if prevState == 0 && lPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}
	return true
}

// yamlInitGeneral loads the shared crawler.yaml configuration.
func yamlInitGeneral() error {
	if configFile != "" {
		viper.SetConfigFile(configFile)
	} else {
		viper.SetConfigName("crawler")
		viper.AddConfigPath(".")
	}
	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		return fmt.Errorf("fatal error config file: %w", err)
	}
	regexBannedDomains = stringlib.RmNewLines(viper.GetString("regexBannedDomains"))
	regexLinkBannedTokens = stringlib.RmNewLines(viper.GetString("regexLinkBannedTokens"))
	engStopWordsWOthe = stringlib.RmNewLines(viper.GetString("engStopWordsWOthe"))
	engStopWords = `the|` + engStopWordsWOthe
	engLowRelevancyWords = `|` + stringlib.RmNewLines(viper.GetString("engLowRelevancyWords"))
	regexStopwords = `(?i)\W([0-9]+|.|..|` + engStopWordsWOthe + engLowRelevancyWords + `|` + stringlib.RmNewLines(viper.GetString("specialStopwords")) + `)\W`
	downloadTimeout = time.Duration(viper.GetInt("downloadTimeout")) * time.Second
	if maxDownloadMB := viper.GetInt("maxDownloadMB"); maxDownloadMB > 0 {
		maxDownloadBytes = int64(maxDownloadMB) << 20
	}
	addUrl = strings.TrimSpace(viper.GetString("addUrl"))
	cacheBackend = strings.TrimSpace(viper.GetString("cacheBackend"))
	if cacheBackend == "" {
		cacheBackend = storelib.BackendLocal
	}
	cachePath = strings.TrimSpace(viper.GetString("cachePath"))
	if cachePath == "" {
		cachePath = "./cache/cache.db"
	}
	logLevel = strings.TrimSpace(viper.GetString("logLevel"))
	if logLevel == "" {
		logLevel = "info"
	}
	respectRobots = true
	if viper.IsSet("respectRobots") {
		respectRobots = viper.GetBool("respectRobots")
	}
	userAgent = strings.TrimSpace(viper.GetString("userAgent"))
	if userAgent == "" {
		userAgent = "goCrawler/1.0 (+https://github.com/computerphysicslab/goCrawler)"
	}

	fmt.Printf("\n\nlogLevel: %s", logLevel)
	fmt.Printf("\nrespectRobots: %v", respectRobots)
	fmt.Printf("\nuserAgent: %s", userAgent)
	fmt.Printf("\n\ncacheBackend: %s", cacheBackend)
	fmt.Printf("\ncachePath: %s", cachePath)
	fmt.Printf("\n\nregexBannedDomains: %s", regexBannedDomains)
	fmt.Printf("\n\nregexLinkBannedTokens: %s", regexLinkBannedTokens)
	fmt.Printf("\n\nengStopWordsWOthe: %s", engStopWordsWOthe)
	fmt.Printf("\n\nengStopWords: %s", engStopWords)
	fmt.Printf("\n\nengLowRelevancyWords: %s", engLowRelevancyWords)
	fmt.Printf("\n\nregexStopwords: %s", regexStopwords)
	fmt.Printf("\n\ndownloadTimeout: %+v", downloadTimeout)
	fmt.Printf("\n\naddUrl: %s", addUrl)
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

	proxyHost = viper.GetString("proxyHost")
	proxyUser = viper.GetString("proxyUser")
	proxyPass = viper.GetString("proxyPass")

	fmt.Printf("\n\nproxyHost: %s", proxyHost)
	fmt.Printf("\n\nproxyUser: %s", proxyUser)
	return nil
}

// yamlInitSpecific loads the profile selected on the command line.

func yamlInitSpecific() error {
	vp := viper.New()
	vp.SetConfigName(profileName)
	if iolib.FileExists("./profiles/" + profileName + ".yaml") {
		vp.AddConfigPath("./profiles")
	} else {
		vp.AddConfigPath(".")
	}
	if err := vp.ReadInConfig(); err != nil {
		return fmt.Errorf("fatal error config file: %w", err)
	}
	fmt.Printf("\n\nprofile file: %s", vp.ConfigFileUsed())
	curatedDomains = stringlib.RmNewLines(vp.GetString("curatedDomains"))
	regexLinkOk = `(?i)^https*://.*(` + stringlib.RmNewLines(vp.GetString("linkOk")) + `|` + curatedDomains + `)`
	regexRankingKeywords = stringlib.RmNewLines(vp.GetString("regexRankingKeywords"))
	bootstrapingLinks = vp.GetStringSlice("bootstrapingLinks")
	minDocLen = vp.GetInt("minDocLen")
	maxDocLen = vp.GetInt("maxDocLen")
	scoreThreshold = float64(vp.GetInt("scoreThreshold"))
	maxLinksPerPage = vp.GetInt("maxLinksPerPage")
	if ms := vp.GetInt("crawlDelayMs"); ms > 0 {
		crawlDelay = time.Duration(ms) * time.Millisecond
	}
	language := strings.TrimSpace(vp.GetString("language"))
	if language != "" {
		targetLanguage = language
	}
	minLanguageConfidence = vp.GetFloat64("minLanguageConfidence")
	rankingStrategy = strings.ToLower(strings.TrimSpace(vp.GetString("rankingStrategy")))
	if rankingStrategy == "" {
		rankingStrategy = "hybrid"
	}
	synonyms = parseSynonyms(vp.GetString("synonyms"))
	buildRankingRegex()
	if maxLinksPerPage <= 0 {
		maxLinksPerPage = 200
	}

	fmt.Printf("\n\nprofileName: %s", profileName)
	fmt.Printf("\n\ncuratedDomains: %s", curatedDomains)
	fmt.Printf("\n\nregexLinkOk: %s", regexLinkOk)
	fmt.Printf("\n\nregexRankingKeywords: %s", regexRankingKeywords)
	fmt.Printf("\n\nbootstrapingLinks: %+v", bootstrapingLinks)
	return nil
}

var searchDB *sql.DB

// initSearchIndex opens the SQLite FTS5 index for the curated corpus.
func initSearchIndex() error {
	if searchDB != nil {
		return nil
	}
	db, err := sql.Open("sqlite", cachePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS corpus_fts USING fts5(url UNINDEXED, text)`); err != nil {
		db.Close()
		return err
	}
	searchDB = db
	return nil
}

// indexDocument adds or replaces a document in the full-text index.
func indexDocument(docURL, text string) {
	if searchDB == nil {
		return
	}
	searchDB.Exec(`DELETE FROM corpus_fts WHERE url = ?`, docURL)
	if _, err := searchDB.Exec(`INSERT INTO corpus_fts(url, text) VALUES(?, ?)`, docURL, text); err != nil {
		logger.Debug("cannot index document", "url", docURL, "error", err)
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
	rows, err := searchDB.Query(`SELECT url, snippet(corpus_fts, 1, '[', ']', ' ... ', 16)
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

const appVersion = "1.0.0"

var profileName string
var targetLanguage = "English"
var minLanguageConfidence float64
var cliDryRun bool
var cliQuery string
var cliExplore bool
var cliGenerate string
var cliWorkers = 1
var configFile string
var stopRequested atomic.Bool

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
		case strings.HasPrefix(arg, "-config="), strings.HasPrefix(arg, "--config="):
			configFile = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-profile="), strings.HasPrefix(arg, "--profile="):
			profileName = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-backend="), strings.HasPrefix(arg, "--backend="):
			cacheBackend = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-cache="), strings.HasPrefix(arg, "--cache-path="):
			cachePath = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-log-level="), strings.HasPrefix(arg, "--log-level="):
			logLevel = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-max-links="):
			v, err := strconv.Atoi(strings.SplitN(arg, "=", 2)[1])
			if err != nil {
				return fmt.Errorf("invalid -max-links: %w", err)
			}
			maxLinksPerPage = v
		case strings.HasPrefix(arg, "-workers="):
			v, err := strconv.Atoi(strings.SplitN(arg, "=", 2)[1])
			if err != nil || v < 1 || v > 32 {
				return fmt.Errorf("invalid -workers (1..32): %s", arg)
			}
			cliWorkers = v
		case strings.HasPrefix(arg, "-query="), strings.HasPrefix(arg, "--query="):
			cliQuery = strings.SplitN(arg, "=", 2)[1]
		case strings.HasPrefix(arg, "-generate-profile="):
			cliGenerate = strings.SplitN(arg, "=", 2)[1]
		case arg == "-dry-run", arg == "--dry-run":
			cliDryRun = true
		case arg == "-explore", arg == "--explore":
			cliExplore = true
		case arg == "-version", arg == "--version":
			fmt.Printf("goCrawler %s\n", appVersion)
			return errStop
		case arg == "-license", arg == "--license":
			fmt.Println("MIT License - Copyright (c) 2020-2026 Juan Ignacio Perez Sacristan")
			return errStop
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag: %s", arg)
		default:
			if profileName == "" {
				profileName = arg
			}
		}
	}
	if profileName == "" && cliQuery == "" && !cliExplore && cliGenerate == "" {
		return fmt.Errorf("profile argument required")
	}
	return nil
}

// errStop signals that a informational flag was handled and the program should exit cleanly.
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

// usage prints the command-line usage message.
func usage() {
	fmt.Println("Usage: go run crawler.go <profile>")
	fmt.Println("  <profile>  profile name (without .yaml), from profiles/ or the working directory")
	fmt.Println("  --selftest run built-in checks and exit")
	fmt.Println("  --resume   restore a previous crawl from the cache store")
	fmt.Println("  -profile=, -backend=, -cache=, -log-level=, -workers=N, -max-links=N")
	fmt.Println("  -dry-run, -explore, -query=TEXT, -generate-profile=URL, -version, -license")
	fmt.Println("Examples: go run crawler.go crypto --resume | go run crawler.go -query=drizzle")
}

// startSessionOutput tees stdout to the terminal and to a timestamped file in logs/.
// It returns a function that restores stdout and closes the file.
func startSessionOutput(profileName string) func() {
	now := time.Now().UTC()
	name := profileName
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
func run() error {
	if len(os.Args) >= 2 && os.Args[1] == "--selftest" {
		if runSelfTests() {
			return nil
		}
		return fmt.Errorf("self-test failed")
	}

	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		if len(os.Args) < 2 {
			return fmt.Errorf("profile argument required")
		}
		return nil
	}

	for _, arg := range os.Args[1:] {
		if arg == "--resume" || arg == "-resume" {
			resumeEnabled = true
		}
	}
	if err := parseCLI(); err != nil {
		if err == errStop {
			return nil
		}
		return err
	}
	if cachePath == "" {
		cachePath = "./cache/cache.db"
	}
	if cacheBackend == "" {
		cacheBackend = "local"
	}
	if logLevel == "" {
		logLevel = "info"
	}
	if cliGenerate != "" {
		return generateProfile(cliGenerate, "./profiles/generated.yaml")
	}
	if cliQuery != "" {
		return runSearch(cliQuery)
	}
	if cliExplore {
		return exploreCorpus()
	}

	// Create needed folders if don't exist
	fmt.Println("* Checking logs folder ...")
	if _, err := os.Stat("./logs"); os.IsNotExist(err) {
		// Logs folder does not exist, we create it automatically
		os.MkdirAll("./logs", os.ModePerm)
	}
	fmt.Println("* Checking output folder ...")
	if _, err := os.Stat("./output"); os.IsNotExist(err) {
		// Output folder does not exist, we create it automatically
		os.MkdirAll("./output", os.ModePerm)
	}

	stopSessionOutput := startSessionOutput(os.Args[1])
	defer stopSessionOutput()

	if err := initFileLoggers(); err != nil {
		return err
	}
	defer closeFileLoggers()
	initRejectedLinkSets()

	fmt.Println("* Loading YAML config ...")
	if err := yamlInitGeneral(); err != nil {
		return err
	}
	initLogger(logLevel)
	if err := yamlInitProxy(); err != nil {
		return err
	}
	initHTTPClient()
	if err := yamlInitSpecific(); err != nil {
		return err
	}
	compileConfigRegexes()

	fmt.Println("* Init cache store ...")
	var err error
	cacheStore, err = storelib.Init(cacheBackend, cachePath)
	if err != nil {
		return fmt.Errorf("cache store error: %w", err)
	}
	defer cacheStore.Close()
	fmt.Printf("Cache backend: %s\n", cacheStore.Name())
	if err := initSearchIndex(); err != nil {
		return fmt.Errorf("search index: %w", err)
	}
	defer func() {
		if searchDB != nil {
			searchDB.Close()
			searchDB = nil
		}
	}()

	startIndex := 0
	if resumeEnabled {
		startIndex = loadCrawlState()
	} else if err := cacheStore.Set(crawlStateKey, ""); err != nil {
		logger.Warn("cannot clear previous crawl state", "error", err)
	}
	defer saveCrawlState()

	if cliDryRun {
		fmt.Printf("Dry run: profile=%s backend=%s cache=%s language=%s strategy=%s workers=%d\n",
			profileName, cacheBackend, cachePath, targetLanguage, rankingStrategy, cliWorkers)
		return nil
	}

	if respectRobots {
		fmt.Println("* Robots policy enabled ...")
		robotsPolicy = robotspolicy.New(userAgent, downloadTimeout)
	}

	fmt.Println("* Init English corpus ...")
	corpusfreqlib.Init()

	if !resumeEnabled {
		fmt.Println("* Link bootstrapping ...")
		linkBootstraping()
	} else {
		fmt.Printf("* Resuming with %d links in the pool ...\n", len(lPool))
	}

	fmt.Println("* Init CSV ...")
	if err := csvInit(); err != nil {
		return err
	}
	defer func() {
		if csvFile != nil {
			csvFile.Close()
		}
	}()

	if !resumeEnabled {
		fmt.Println("* Init curated corpus ...")
		iolib.String2file("\n", "./logs/corpusCuratedText.log")
	}

	// Graceful stop on Ctrl-C: workers finish the current page and persist state.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupt received, saving state...")
		stopRequested.Store(true)
	}()

	// Loop: one goroutine per worker; downloads overlap, shared state is mutex-protected.
	var nextIndex atomic.Int64
	nextIndex.Store(int64(startIndex))
	var wg sync.WaitGroup
	for i := 0; i < cliWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if stopRequested.Load() {
					return
				}
				index := int(nextIndex.Add(1) - 1)
				crawlMu.Lock()
				lastProcessedLink = index
				crawlMu.Unlock()
				if !doNextLink(index) {
					return
				}
			}
		}()
	}
	wg.Wait()
	crawlMu.Lock()
	finalIndex := lastProcessedLink
	crawlMu.Unlock()

	dumpFrequencies(finalIndex, true)
	logger.Info("crawl finished",
		"processed", lastProcessedLink,
		"accepted", acceptedDocs,
		"rejections", rejectionSummary())
	fmt.Println("\n\n\n***** Done!!!")
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
	engStopWordsWOthe = "a|and|of|the|with"
	engStopWords = "the|" + engStopWordsWOthe
	engLowRelevancyWords = "|news|info"
	regexStopwords = `(?i)\W([0-9]+|.|..|` + engStopWordsWOthe + engLowRelevancyWords + `)\W`
	regexRankingKeywords = `(?i)\W(stack|stacking|image|noise|signal|siril)\W`
	regexLinkOk = `(?i)^https*://.*(stack|siril|astro|image)`
	regexBannedDomains = `(?i)(facebook|twitter)\.(com|org)`
	regexLinkBannedTokens = `(?i)(login|signup|pdf|\.(pdf|jpg))`
	compileConfigRegexes()
	rankingStrategy = "hybrid"
	synonyms = make(map[string][]string)
	buildRankingRegex()
	corpusDocFreq = make(map[string]int)
	corpusDocCount = 0
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

	corpusfreqlib.Init()
	check("English corpus", corpusfreqlib.Freq("the") > 0, &allOK)
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
	for name := range rejectedLinkSets {
		rejectedLinkSets[name] = make(map[string]struct{})
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
