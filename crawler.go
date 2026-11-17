// Package main implements a topic-restricted web crawler that builds curated English corpora.
package main

import (
	"bytes"
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
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
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
var scoreThreshold float64
var addUrl string
var cacheBackend, cachePath string
var cacheStore storelib.Store
var robotsPolicy *robotspolicy.Policy
var userAgent string
var respectRobots bool
var logLevel string

// logger is replaced at startup so records are written through the session tee.
var logger = slog.New(slog.NewTextHandler(os.Stdout, nil))

var rejectionCounts = make(map[string]int)
var acceptedDocs int
var lastProcessedLink int
var resumeEnabled bool

const crawlStateKey = "state:crawl"

// crawlState is the serializable snapshot needed to resume a run.
type crawlState struct {
	Pool          []aLink           `json:"pool"`
	DomainCounter map[string]int    `json:"domainCounter"`
	Processed     int               `json:"processed"`
	UniqueSigs    map[string]string `json:"uniqueSignatures"`
	Frequencies   map[string]int    `json:"frequencies"`
	SavedAt       time.Time         `json:"savedAt"`
}

// saveCrawlState stores the current crawl progress in the cache store.
func saveCrawlState() {
	state := crawlState{
		Pool:          lPool,
		DomainCounter: domainCounter,
		Processed:     lastProcessedLink,
		UniqueSigs:    uniqueSignature,
		Frequencies:   corpusFreqs,
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
	lPool = state.Pool
	domainCounter = state.DomainCounter
	uniqueSignature = state.UniqueSigs
	corpusFreqs = state.Frequencies
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

// countRejection increments the counter for one rejection reason.
func countRejection(reason string) {
	rejectionCounts[reason]++
}

// rejectionSummary returns the rejection counters as log attributes.
func rejectionSummary() []interface{} {
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
func csvInit() {
	var file *os.File
	var err error
	if resumeEnabled {
		file, err = os.OpenFile("output/ranking-URL.csv", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	} else {
		file, err = os.Create("output/ranking-URL.csv")
	}
	if err != nil {
		log.Fatal("Cannot create file: ", err)
	}
	csvFile = file
	csvWriter = csv.NewWriter(file)
	csvWriter.Comma = '\t'
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

type CachedData struct {
	Content string
	Links   []string
}

var domainCounter = make(map[string]int)

// proxyGet performs an HTTP GET with the configured timeout and optional proxy.
func proxyGet(urlLink string, printLog bool) (resp *http.Response, err error) {
	var client *http.Client
	if proxyHost != "" {
		client = &http.Client{
			Timeout: downloadTimeout,
			Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{
				Scheme: "http",
				User:   url.UserPassword(proxyUser, proxyPass),
				Host:   proxyHost,
			})}}
	} else {
		client = &http.Client{
			Timeout: downloadTimeout,
		}
	}
	req, err := http.NewRequest("GET", urlLink, nil)
	if err != nil {
		return
	}

	// To avoid EOF errors
	req.Close = true

	if printLog {
		fmt.Printf("\nStart downloading")
	}
	resp, err = client.Do(req)
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
	var ACachedData CachedData

	// Get results from the configured cache store (SQLite by default).
	cacheKey := "cache_" + urlLink
	found := cacheStore.Exists(cacheKey)
	if found {
		cachedValue := cacheStore.Get(cacheKey)
		if cachedValue == "" {
			found = false
		} else {
			err = json.Unmarshal([]byte(cachedValue), &ACachedData)
			if err != nil {
				panic(err)
			}

			fmt.Printf(" [CACHE]")
			err = nil
		}
	}
	if !found {
		if robotsPolicy != nil {
			if !robotsPolicy.Allowed(urlLink) {
				countRejection("robots_blocked")
				logRejectedLink("robotsBlocked.log", urlLink)
				fmt.Printf(" [ROBOTS-BLOCKED]")
				return "", nil, errors.New("download: blocked by robots.txt")
			}
			robotsPolicy.Wait(urlLink)
		}
		ACachedData.Content, ACachedData.Links, err = download(urlLink)
		if err == nil {
			encoded, marshalErr := json.Marshal(ACachedData)
			if marshalErr != nil {
				fmt.Printf(" [CACHE MARSHAL ERROR: %v]", marshalErr)
			} else if setErr := cacheStore.Set(cacheKey, string(encoded)); setErr != nil {
				fmt.Printf(" [CACHE WRITE ERROR: %v]", setErr)
			}
			fmt.Printf(" [ONLINE]")
		}
	}

	content = ACachedData.Content
	links = ACachedData.Links

	return
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
	var re = regexp.MustCompile(`^www\.(.*)$`)
	mainDomain = re.ReplaceAllString(mainDomain, `$1`)

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
	var re = regexp.MustCompile(`^.*?([^\.]+\.[^\.]+)$`)
	mainDomain = re.ReplaceAllString(mainDomain, `$1`)

	return mainDomain
}

// increaseDomainCounter records one more visit for a domain.
func increaseDomainCounter(domain string) {
	domainCounter[domain]++
}

// isBanned reports whether a link matches the configured blacklists.
func isBanned(link string, domain string) bool {
	rd, _ := regexp.Compile(regexBannedDomains)
	if len(rd.FindStringSubmatch(domain)) > 0 {
		return true
	}

	rt, _ := regexp.Compile(regexLinkBannedTokens)
	if len(rt.FindStringSubmatch(link)) > 0 {
		return true
	}

	return false
}

const maxUniqueRejectedLinks = 1000000

var rejectedLinkSets = map[string]map[string]struct{}{
	"linksNotOk.log":                 {},
	"linksNotOk.additional.log":      {},
	"linksNotOk.ParseRequestURI.log": {},
	"robotsBlocked.log":              {},
}

// initRejectedLinkSets loads existing rejected links to deduplicate across runs.
func initRejectedLinkSets() {
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
	regexURL := `(?i)(^http(s)?://[a-z0-9-]+(\.[a-z0-9-]+)*(:[0-9]+)?(\/[a-z0-9\-\_\/\.\+\%\(\)\~\@]*)?$)`
	rURL := regexp.MustCompile(regexURL)
	matches := rURL.FindAllStringSubmatch(l, -1)
	if len(matches) < 1 {
		countRejection("invalid_url")
		logRejectedLink("linksNotOk.additional.log", l)
		return false
	}

	// Check the URL against the profile whitelist.
	if !avoidCustomCheck {
		r, _ := regexp.Compile(regexLinkOk)
		match := r.FindStringSubmatch(l)
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

	increaseDomainCounter(lPool[maxi].Domain)

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
		regex := `\?.*$`
		rs := regexp.MustCompile(regex)
		link2 := rs.ReplaceAllString(link, "")
		if link2 != link {
			logCGI.Printf("\n\n%s\n%s", link2, link)
			link = link2
		}
	}

	// Look for an existing entry in the link pool.
	for i, l := range lPool {
		if l.URL == link {
			lPool[i].Count++
			return true
		}
	}

	// Register a new link in the pool.
	if avoidFilters {
		lPool = append(lPool, aLink{URL: link, Domain: domain, Count: 1, Status: 4})
	} else {
		lPool = append(lPool, aLink{URL: link, Domain: domain, Count: 1, Status: 0})
	}

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

// domainReportFailed records a transport failure for a domain.
func domainReportFailed(domain string) {
	iolib.String2fileAppend(domain+"\n", "./logs/domainFailed.log")
}

// domainHadFailed reports whether a domain exceeded the failure limit.
func domainHadFailed(domain string) bool {
	if domain == "" {
		return false
	}
	target := strings.ToLower(domain)
	count := 0
	for _, line := range strings.Split(iolib.File2string("./logs/domainFailed.log"), "\n") {
		if strings.ToLower(strings.TrimSpace(line)) == target {
			count++
		}
	}
	return count > 6
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
	re := regexp.MustCompile(`[\r\n]+`)
	ar := re.Split(text, -1)

	return ar
}

// rankingByKeywords scores text by unique keywords normalized by document length.
func rankingByKeywords(text string) float64 {
	r, _ := regexp.Compile(regexRankingKeywords)
	rr := r.FindAllStringSubmatch(text, -1)

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
	regex := `(?i)\W(` + engStopWords + `)\W`
	rs := regexp.MustCompile(regex)
	matches := rs.FindAllStringSubmatch(" "+text+" ", -1)

	return len(matches)
}

// stopWordsOnBorderCount counts stopwords at token boundaries.
func stopWordsOnBorderCount(text string) int {
	regexLeft := `(?i)^(` + engStopWords + `)\W`
	rsLeft := regexp.MustCompile(regexLeft)
	matchesLeft := rsLeft.FindAllStringIndex(text, -1)

	regexRight := `(?i)\W(` + engStopWords + `)$`
	rsRight := regexp.MustCompile(regexRight)
	matchesRight := rsRight.FindAllStringIndex(text, -1)

	return len(matchesLeft) + len(matchesRight)
}

// lowRelevancyWordsOnBorderCount counts low-relevancy words at token boundaries.
func lowRelevancyWordsOnBorderCount(text string) int {
	regexLeft := `(?i)^(` + engStopWords + engLowRelevancyWords + `)\W`
	rsLeft := regexp.MustCompile(regexLeft)
	matchesLeft := rsLeft.FindAllStringIndex(text, -1)

	regexRight := `(?i)\W(` + engStopWords + `)$`
	rsRight := regexp.MustCompile(regexRight)
	matchesRight := rsRight.FindAllStringIndex(text, -1)

	return len(matchesLeft) + len(matchesRight)
}

// stopwordFilter removes stopwords from a text.
func stopwordFilter(text string) string {

	// Words must be separated by 2 spaces to regexp find all stopwords
	doubleSpace := `(\W+)`
	r0 := regexp.MustCompile(doubleSpace)
	text = r0.ReplaceAllString(text, `  `)

	rs := regexp.MustCompile(regexStopwords)
	text = fmt.Sprintf(" %s ", text)
	text2 := rs.ReplaceAllString(text, ` `)
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
	r0 := regexp.MustCompile(`(\W+)`)

	// Avoid meaningless characters
	r1 := regexp.MustCompile(`([*\(\)\?\-\,\:\#\[\]\"]+)`)

	// Remove double spaces
	rf := regexp.MustCompile(`(\W)\W`)

	// Find bigrams
	re := regexp.MustCompile(`(?i)\W([^\W]+)\W`)

	// nGrams to ignore
	regexNGramsIgnore := `(?i)\W(cite_note|cite_ref|https*)\W`
	ri := regexp.MustCompile(regexNGramsIgnore)

	for _, p := range uniqueSentenceSignatures {
		// Prepare text
		p = r0.ReplaceAllString(" "+p+" ", `$1$1`)
		p = r1.ReplaceAllString(p, ` `)

		// Find bigrams
		bigramMatches := re.FindAllStringSubmatch(p, -1)

		var previousToken = make(map[int]string)
		for _, aBigramFound := range bigramMatches {
			if len(tokenize(previousToken[n-1])) == n-1 {
				ngramCandidate := strings.TrimSpace(previousToken[n-1] + aBigramFound[0])

				// Remove double spaces
				ngramCandidate = rf.ReplaceAllString(ngramCandidate, `$1`)

				if lowRelevancyWordsOnBorderCount(ngramCandidate) == 0 { // avoid ngrams containing stopwords on left/right border
					if len(ri.FindAllStringIndex(" "+ngramCandidate+" ", -1)) == 0 { // avoid ngrams to ignore
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
func initFileLoggers() {
	outfile, _ = os.Create("./logs/download.log")
	logDownload = log.New(outfile, "", 0)
	outfile3, _ = os.Create("./logs/cgi.log")
	logCGI = log.New(outfile3, "", 0)
	outfile4, _ = os.Create("./logs/domainFailed.log")
	logDomainFailed = log.New(outfile4, "", 0)
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
	}
	fmt.Printf("\n %d links found (%d added)", len(links), linksAdded)
}

// doNextLink processes the next queued link and reports whether crawling should continue.
func doNextLink(numLinksProcessed int) bool {
	maxi, nextLink := getNextLink()

	// Just for debug purposes, to show on screen why this url was accepted
	linkSeemsOk(nextLink, false)

	if nextLink == "" {
		fmt.Println("* No more links available in the pool")
		fmt.Println(maxi)
		return false // meaning there are no more links to explore
	}
	prevState := lPool[maxi].Status
	lPool[maxi].Status = 1
	fmt.Printf("\n\n* Downloading url: %s", nextLink)

	// TODO Some URLs take a lot of time to download or process.
	// Adding log to find out if the issue is coming from the
	// download process or the afterward process.

	content, links, err := downloadCached(nextLink)
	if err != nil {
		lPool[maxi].Status = 3
		fmt.Println("\nDownload error: ", strings.Trim(err.Error(), "\n"))
	} else {
		lPool[maxi].Status = 2
	}

	// Adding links of bootstrapping before filters
	if prevState == 4 && lPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}

	// Tokenizer url keywords frequencies
	// f.add(nextLink)

	// Split content into paragraphs
	paragraphs := splitParagraphs(content)

	// Remove urls, imgs, long words and low stopwords paragraphs from text
	for i, p := range paragraphs {
		// Language detection

		// 	paragraphs[i] = ""

		// 	paragraphs[i] = ""

		whatlanggoInfo := whatlanggo.Detect(p)
		if whatlanggoInfo.Lang.String() != "English" {
			countRejection("non_english")
			paragraphs[i] = ""
			continue
		}

		regex1 := `(?i)\W([^ \t]*/[^ \t]*)\W`
		r1 := regexp.MustCompile(regex1)
		p2 := r1.ReplaceAllString(p, " ")

		regex2 := `(?i)(<(p|img|div)[^>]*>)`
		r2 := regexp.MustCompile(regex2)
		p3 := r2.ReplaceAllString(p2, " ")
		if p2 != p3 {
		}

		regex3 := `(?i)\W([^ \t\n]{80,})\W`
		r3 := regexp.MustCompile(regex3)
		p4 := r3.ReplaceAllString(p3, " ")

		regex4 := `(?i)\W(div|img|nofollow|javascript:|(alt|class|style|width|height|onclick)="[^"]*")\W`
		r4 := regexp.MustCompile(regex4)
		matches4 := r4.FindAllStringSubmatch(p, -1)
		if len(matches4) > 0 {
			paragraphs[i] = ""
			continue
		}
		p5 := p4

		numStopWords := stopWordsCount(p5)
		numTotalWords := len(tokenize(p5))
		ratioStopWords := float64(numStopWords) / float64(numTotalWords+1)
		if ratioStopWords < 0.1 {
			countRejection("stopword_ratio")
			paragraphs[i] = ""
		} else {
			paragraphs[i] = p5
		}
	}

	bParagraph := bestParagraph(paragraphs)

	curatedContent := ""
	for _, p := range paragraphs {
		if len(p) < 200 {
			continue
		}
		ratio := float64(stopWordsCount(p)) / float64(tokensCount(p)+1)
		if ratio < 0.1 || ratio > 0.38 {
			continue
		}
		curatedContent = curatedContent + "\n" + p
	}

	// Doc length
	docLen := len(tokenize(curatedContent))

	// Cut filter on curated numWords
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

	// Current doc frequencies
	fDoc := make(freq)
	fDoc.add(curatedContent)
	// remove "the" frequency
	fDoc["the"] = 0
	gDoc := rSortFreq(fDoc)

	gDocSignature := ""
	if len(gDoc) > 7 {
		gDocSignature = fmt.Sprintf("%v", getKVkeys(gDoc[:7]))
	} else {
		gDocSignature = fmt.Sprintf("%v", getKVkeys(gDoc))
	}

	// Ignore when signature is not unique
	if uniqueSignature[gDocSignature] == "" {
		uniqueSignature[gDocSignature] = nextLink
	} else {
		countRejection("duplicate")
		logger.Debug("document rejected", "reason", "duplicate", "url", nextLink, "duplicate_of", uniqueSignature[gDocSignature])
		return true
	}

	// Ignore when ranking score is lower than cutoff threshold
	s := rankingByKeywords(curatedContent)
	if s < scoreThreshold {
		countRejection("low_relevancy")
		logger.Debug("document rejected", "reason", "low_relevancy", "url", nextLink, "score", s)
		return true
	}

	iolib.String2fileAppend(nextLink+"\n"+curatedContent+"----\n\n\n\n", "./logs/corpusCuratedText.log")

	// Append CSV row
	err = csvWriter.Write([]string{fmt.Sprintf("%.02f", rankingByKeywords(curatedContent)), fmt.Sprintf("%d", docLen), gDocSignature, nextLink, bParagraph})
	if err != nil {
		log.Fatal("Cannot write to file", err)
	}
	csvWriter.Flush()

	// Cut values on maxFreq/numWords ratio 0.1 - 0.002
	if float64(gDoc[0].Value)/float64(1+docLen) > 0.1 || float64(gDoc[0].Value)/float64(1+docLen) < 0.002 {
		countRejection("token_distribution")
		logger.Debug("document rejected", "reason", "token_distribution", "url", nextLink)
		return true
	}
	acceptedDocs++

	// Tokenizer text token frequencies
	corpusFreqs.add(curatedContent)

	// Once in a while...
	if numLinksProcessed%50 == 0 {
		saveCrawlState()
		logger.Info("progress",
			"processed", numLinksProcessed,
			"accepted", acceptedDocs,
			"rejections", rejectionSummary())
		fmt.Printf("\n\nnumLinksProcessed: %d", numLinksProcessed)

		corpusFreqsSorted := rSortFreq(corpusFreqs)
		corpusFreqsSorted_100 := corpusFreqsSorted
		if len(corpusFreqsSorted) > 100 {
			corpusFreqsSorted_100 = corpusFreqsSorted[:100]
		}
		fmt.Println("\n\nCorpus frequencies: ", corpusFreqsSorted_100)

		// Saving corpus frequencies in format all.num from British National Corpus
		output := ""
		for _, gg := range corpusFreqsSorted {
			output = output + fmt.Sprintf("%d %s %s %d\n", gg.Value, gg.Key, "none", 0)
		}
		iolib.String2file(output, "./output/corpusFrequencies.txt")

		// substracting english words frequencies
		corpusFreqsWithoutEnglish := make(freq) // specific corpus token frequencies w/o english baseline

		var intercorpusScaleFactor float64
		intercorpusContrast := 20.0
		if corpusFreqsSorted[0].Key == "the" {
			intercorpusScaleFactor = float64(1+corpusfreqlib.Freq("the")) / float64(corpusFreqsSorted[0].Value)
		} else {
			panic("Error: stopword \"the\" not found!")
		}

		for _, keyValue := range corpusFreqsSorted {
			// By division:
			// corpusFreqsWithoutEnglish[keyValue.Key] = int(intercorpusScaleFactor * float64(keyValue.Value) / float64(1+corpusfreqlib.Freq(keyValue.Key)))
			// By substraction:
			corpusFreqsWithoutEnglish[keyValue.Key] = keyValue.Value - int(intercorpusContrast*float64(1+corpusfreqlib.Freq(keyValue.Key))/intercorpusScaleFactor)
		}
		corpusFreqsWithoutEnglishSorted := rSortFreq(corpusFreqsWithoutEnglish)
		corpusFreqsWithoutEnglishSorted_100 := corpusFreqsWithoutEnglishSorted
		if len(corpusFreqsWithoutEnglishSorted) > 100 {
			corpusFreqsWithoutEnglishSorted_100 = corpusFreqsWithoutEnglishSorted[:100]
		}
		fmt.Println("\n\nCorpus frequencies w/o Eng.: ", corpusFreqsWithoutEnglishSorted_100)

		// Saving corpus w/o English frequencies in basic format
		output = ""
		for _, gg := range corpusFreqsWithoutEnglishSorted {
			output = output + fmt.Sprintf("%d %s\n", gg.Value, gg.Key)
		}
		iolib.String2file(output, "./output/corpusNoEngFrequencies.txt")

		// LPool dump to file
		lPoolDump()
		domainCounterDump()

		// doc, _ := prose.NewDocument(corpusCuratedText)
		// 	entityFreq[ent.Text+" :: "+ent.Label]++

	}

	// push content into persitent ddbb
	// save(curatedContent, l.URL)

	// Request external search engine to index the url
	if len(addUrl) > 0 {
		requestIndexUrl := addUrl + url.QueryEscape(nextLink)
		fmt.Printf("\nrequestIndexUrl: %s", requestIndexUrl)

		// Call indexer as a gorutine for concurrency
		go func(aUrl string) {
			_, err = proxyGet(aUrl, false)
			if err != nil {
				// logDownload.Printf("\tHttp transport error:\n\t\t%s", err)
				fmt.Printf("\n\nHttp transport error:\n\t\t%s", err)
			}
		}(requestIndexUrl)
	}

	// Adding links of urls passing filters
	if prevState == 0 && lPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}

	return true
}

// yamlInitGeneral loads the shared crawler.yaml configuration.
func yamlInitGeneral() {
	viper.SetConfigName("crawler") // name of config file (without extension)
	viper.AddConfigPath(".")       // look for config in the working directory
	err := viper.ReadInConfig()    // Find and read the config file
	if err != nil {                // Handle errors reading the config file
		panic(fmt.Errorf("Fatal error config file: %s", err))
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
}

// yamlInitProxy loads the optional proxy.yaml settings.
func yamlInitProxy() {
	if !iolib.FileExists("./proxy.yaml") {
		return
	}
	viper.SetConfigName("proxy") // name of config file (without extension)
	viper.AddConfigPath(".")     // look for config in the working directory
	err := viper.ReadInConfig()  // Find and read the config file
	if err != nil {              // Handle errors reading the config file
		panic(fmt.Errorf("Fatal error config file: %s", err))
	}

	proxyHost = viper.GetString("proxyHost")
	proxyUser = viper.GetString("proxyUser")
	proxyPass = viper.GetString("proxyPass")

	fmt.Printf("\n\nproxyHost: %s", proxyHost)
	fmt.Printf("\n\nproxyUser: %s", proxyUser)
}

// yamlInitSpecific loads the profile selected on the command line.
// yamlInitSpecific loads the profile selected on the command line.
func yamlInitSpecific() {
	profileName := os.Args[1]
	vp := viper.New()
	vp.SetConfigName(profileName)
	if iolib.FileExists("./profiles/" + profileName + ".yaml") {
		vp.AddConfigPath("./profiles")
	} else {
		vp.AddConfigPath(".")
	}
	if err := vp.ReadInConfig(); err != nil {
		panic(fmt.Errorf("Fatal error config file: %s", err))
	}
	fmt.Printf("\n\nprofile file: %s", vp.ConfigFileUsed())
	curatedDomains = stringlib.RmNewLines(vp.GetString("curatedDomains"))
	regexLinkOk = `(?i)^https*://.*(` + stringlib.RmNewLines(vp.GetString("linkOk")) + `|` + curatedDomains + `)`
	regexRankingKeywords = stringlib.RmNewLines(vp.GetString("regexRankingKeywords"))
	bootstrapingLinks = vp.GetStringSlice("bootstrapingLinks")
	minDocLen = vp.GetInt("minDocLen")
	maxDocLen = vp.GetInt("maxDocLen")
	scoreThreshold = float64(vp.GetInt("scoreThreshold"))

	fmt.Printf("\n\nprofileName: %s", profileName)
	fmt.Printf("\n\ncuratedDomains: %s", curatedDomains)
	fmt.Printf("\n\nregexLinkOk: %s", regexLinkOk)
	fmt.Printf("\n\nregexRankingKeywords: %s", regexRankingKeywords)
	fmt.Printf("\n\nbootstrapingLinks: %+v", bootstrapingLinks)
}

// usage prints the command-line usage message.
func usage() {
	fmt.Println("Usage: go run crawler.go <profile>")
	fmt.Println("  <profile>  profile name (without .yaml), from profiles/ or the working directory")
	fmt.Println("  --selftest run built-in checks and exit")
	fmt.Println("  --resume   restore a previous crawl from the cache store")
	fmt.Println("Example: go run crawler.go crypto --resume")
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
func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--selftest" {
		if runSelfTests() {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		if len(os.Args) < 2 {
			os.Exit(1)
		}
		return
	}

	for _, arg := range os.Args[2:] {
		if arg == "--resume" {
			resumeEnabled = true
		}
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

	initFileLoggers()
	defer closeFileLoggers()
	initRejectedLinkSets()

	fmt.Println("* Loading YAML config ...")
	yamlInitGeneral()
	initLogger(logLevel)
	yamlInitProxy()
	yamlInitSpecific()

	fmt.Println("* Init cache store ...")
	var err error
	cacheStore, err = storelib.Init(cacheBackend, cachePath)
	if err != nil {
		fmt.Println("Cache store error: ", err)
		os.Exit(1)
	}
	defer cacheStore.Close()
	fmt.Printf("Cache backend: %s\n", cacheStore.Name())

	startIndex := 0
	if resumeEnabled {
		startIndex = loadCrawlState()
	} else if err := cacheStore.Set(crawlStateKey, ""); err != nil {
		logger.Warn("cannot clear previous crawl state", "error", err)
	}
	defer saveCrawlState()

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
	csvInit()
	defer func() {
		if csvFile != nil {
			csvFile.Close()
		}
	}()

	if !resumeEnabled {
		fmt.Println("* Init curated corpus ...")
		iolib.String2file("\n", "./logs/corpusCuratedText.log")
	}

	// Loop
	for numLinksProcessed := startIndex; ; numLinksProcessed++ {
		lastProcessedLink = numLinksProcessed
		if !doNextLink(numLinksProcessed) {
			break
		}
	}

	logger.Info("crawl finished",
		"processed", lastProcessedLink,
		"accepted", acceptedDocs,
		"rejections", rejectionSummary())
	fmt.Println("\n\n\n***** Done!!!")
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
