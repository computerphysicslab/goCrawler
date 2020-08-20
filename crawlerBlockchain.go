// aggregates blockchain related research documents
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/gob"
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
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackdanger/collectlinks"
	snowballeng "github.com/kljensen/snowball/english"
	"github.com/patrickmn/go-cache"
	"jaytaylor.com/html2text"
)

/******************************************************************************/
/******************************************************************************/
/*********************** CONFIG ***********************************************/
/******************************************************************************/
/******************************************************************************/

var regexBannedDomains string = `(?i)((facebook|twitter|reddit|instagram|google|youtube|urldefense|thesexyouwant)\.(com|org)|archive\.org|repubblica\.it|(^en)\.wikipedia\.org)`

var regexLinkBannedTokens string = `(?i)(login|signup|pdf|\.(pdf|ps|xls|ods|csv|json|png|jpg|gif|zip|tar|gz|iso|rar|mp3|wav|avi|mpeg|mpg|mp4|mov|docx|exe|7z|ppt))`

var curatedDomains string = `en\.wikipedia\.org|cointelegraph|coindesk|medium|ethereum|bitcoin\.stackexchange|ethereum\.stackexchange|forbes\.com|fortune\.com` +
	`|arxiv\.org|wired\.com`

var regexLinkOk string = `(?i)^https*://.*(blockchain|crypto|ethereum|trustless|hyperledger|decentralized|ledger|smart-*contract|hash` +
	`|bitcoin|trading|assets*|financial|transactions*|trust|cash|fintech|` + curatedDomains + `)`

var engStopWords string = `a|and|be|have|i|in|of|that|the|to|with|from|is|on|up|for|should|even|why|by|during|we|could|but|about|as|or|this|at|not|all|other` +
	`|if|can|how|may|who|an|no|our|what|use|get|will|has|their|was|than|which|these|also|been|when|through|were|under|there|those|out|after|such|any|before` +
	`|here|only|some|its|where|into|like|would|against|between|most|so|over|because|now|while|since|however|non|without|among|both|another|still|just|way|very` +
	`|good|around|every|each|his|her|then|much|less|few|same|within|per|whether`

var engLowRelevancyWords string = `|articles*|publications*|questions*|times|data|source|people|information|news*|search|content|home|sites*|best|well|pdf|files` +
	`|uploads|programs*|support|help|default|files*|available|please|including|website|related|work|number|days*|using|two|ref|first|daily|public|cases*|high|possible` +
	`|system|review|based|provide|results|additional|include|current|important|week|group|full|different|person|take|continue|national|needs*|millions*|requiremets*|working` +
	`|you|your|more|says|read|make|made|see|does|due|she|one|said|being|had|need|them|many|used|must|do|they|it|he|are|twitter|facebook|date|time|pages*|topics*|example` +
	`|things|real|wiki|early|year|currently|higher|specific`

var regexStopwords string = `(?i)\W([0-9]+|.|..|` + engStopWords + `|` + engLowRelevancyWords +
	`|https*|www|php|aspx|index|en|html` +
	`|january|february|march|april|may|june|july|august|september|october|november|december` +
	`|com|org|gov|uk|edu|net|us|co|gob|au|ca)\W`

var regexRankingKeywords string = `(?i)\W(blockchain|ldt|crypto|ethereum|trustless|hyperledger|libra|decentralized|ledger)\W`

var proxyHost string = ""
var proxyUser string = ""
var proxyPass string = ""

// Pages w/ great links
var bootstrapingLinks = []string{
	"https://en.wikipedia.org/wiki/Blockchain",
	"https://cointelegraph.com/news/chinas-national-blockchain-network-launches-international-website",
	"https://davidgerard.co.uk/blockchain/2020/08/06/news-a-fool-and-his-money-are-soon-decentralised-kodak-rides-again-ledger-data-breach-100k-bitcoin/",
	"https://www.cnbc.com/2020/08/06/goldman-names-new-head-of-digital-assets-in-bet-that-blockchain-is-the-future-of-financial-markets.html",
}

/******************************************************************************/
/******************************************************************************/
/*********************** FUNCTIONS ********************************************/
/******************************************************************************/
/******************************************************************************/

func interface2string(i interface{}) string {
	pretty, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		fmt.Println("error: ", err)
	}
	return string(pretty)
}

func myDebug_static(s string, i interface{}) {
	pretty := interface2string(i)
	fmt.Printf("\nmyDebug: %s (%s) => %s\n", s, reflect.TypeOf(i).String(), string(pretty))
	// fmt.Printf("\n\nmyDebug: %s RAW => %s", s, i)
	// fmt.Printf("\n\nmyDebugPlain: %+v\n", i)
}

func myDebug(params ...interface{}) {
	if len(params) == 0 {
		fmt.Println("\nError: not enough parameters when calling myDebug function")
	} else if len(params) == 1 {
		myDebug_static("", params[0])
	} else if len(params) == 2 {
		switch params[0].(type) {
		case string:
			myDebug_static(params[0].(string), params[1])
		default:
			myDebug_static("", params[0])
			myDebug_static("", params[1])
		}
	} else {
		fmt.Println("\nError: too many parameters when calling myDebug function")
	}
}

type ALink struct {
	Url    string
	Domain string
	Count  int
	Status int // 0 = pending, 1 = crawling, 2 = downloaded, 3 = failed
}

var LPool []ALink

type CachedData struct {
	Content string
	Links   []string
}

var domainCounter = make(map[string]int)

// A cache to avoid repeated HTTP calls from crawlers
var myCache *cache.Cache

func cacheInit() {
	// Load serialized cache from file if exists
	b, err := ioutil.ReadFile("./cachePersistent.dat")
	if err != nil {
		myCache = cache.New(5*time.Minute, 10*time.Minute)
		return
	}

	// Deserialize
	decodedMap := make(map[string]cache.Item, 500)
	d := gob.NewDecoder(bytes.NewBuffer(b))
	err = d.Decode(&decodedMap)
	if err != nil {
		panic(err)
	}

	myCache = cache.NewFrom(5*time.Minute, 10*time.Minute, decodedMap)
}

// copyFileContents copies the contents of the file named src to the file named
// by dst. The file will be created if it does not already exist. If the
// destination file exists, all it's contents will be replaced by the contents
// of the source file.
func copyFileContents(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return
	}
	err = out.Sync()
	return
}

var saveBackupCount int // to static...

// Store cache into persistent file
func cacheSave() {
	// save a backup from time to time
	saveBackupCount++
	if saveBackupCount%10 == 0 {
		err := copyFileContents("./cachePersistent.dat", "./cachePersistent.backup")
		if err != nil {
			panic(err)
		}
		fmt.Println("\n\n##################################### BACKUP ##########################################\n")
	}

	// Serialize cache
	b := new(bytes.Buffer)
	e := gob.NewEncoder(b)
	// Encoding the map
	err := e.Encode(myCache.Items())
	// myDebug(myCache.Items())
	if err != nil {
		panic(err)
	}

	// Save serialized cache into file
	err = ioutil.WriteFile("./cachePersistent.dat", b.Bytes(), 0644)
	if err != nil {
		panic(err)
	}
}

func proxyGet(urlLink string) (resp *http.Response, err error) {
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
	// if err != nil {
	// 	panic(fmt.Errorf("http.NewRequest error: %s", err))
	// }

	// To avoid EOF errors
	req.Close = true

	resp, err = client.Do(req)

	// defer req.Body.Close()

	return
}

func download(urlLink string) (string, []string, error) {
	logDownload.Printf("\n%s", urlLink)
	domain := getDomain(urlLink)
	if domainHadFailed(domain) {
		logDownload.Printf("\tPreviously failed")
		return "", nil, errors.New("download: Previously failed")
	}
	logDownload.Printf("\tRequested")

	// resp, err := http.Get(urlLink)
	resp, err := proxyGet(urlLink)
	if err != nil {
		logDownload.Printf("\tHttp transport error:\n\t\t%s", err)
		domainReportFailed(domain)
		return "", nil, err
	}

	// Get content
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logDownload.Printf("\t%s%s", "Read error: ", err)
		return "", nil, err
	}
	resp.Body.Close() // must close
	resp.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))
	// fmt.Printf("\n\nresp.Body: %+v", resp.Body)

	// Get links
	links := collectlinks.All(resp.Body) // Here we use the collectlinks package
	// fmt.Printf("\n\nlinks: %+v", links)

	// for _, link := range links {
	// 	fmt.Println(link)
	// }

	// Save html content to disk
	// err = ioutil.WriteFile("./output.html", bodyBytes, 0644)
	// if err != nil {
	// 	return "", nil, err
	// 	// panic(fmt.Errorf("Saving html to disk: %s", err))
	// }

	// Get plain text content
	// plain, err := html2text.FromString(string(bodyBytes), html2text.Options{PrettyTables: true})
	plain, err := html2text.FromString(string(bodyBytes), html2text.Options{PrettyTables: false})
	// fmt.Printf("\n\nplain: %+v", plain)
	if err != nil {
		logDownload.Printf("\t%s%s", "html2text.FromString fails: ", err)
		return "", nil, err
	}

	// fmt.Println(plain)
	logDownload.Printf("\t%s len(plain): %d len(links): %d", "Ok: ", len(plain), len(links))

	return plain, links, nil
}

func downloadCached(urlLink string) (content string, links []string, err error) {
	var ACachedData CachedData

	// Get results from cache if available
	b, found := myCache.Get(urlLink)
	if found {
		ACachedData = b.(CachedData)
		fmt.Printf(" [CACHE]")
		// fmt.Println("ACachedData: ", ACachedData)
		err = nil
	} else {
		ACachedData.Content, ACachedData.Links, err = download(urlLink)
		if err == nil {
			myCache.Set(urlLink, ACachedData, cache.NoExpiration) // Store download results in cache
			cacheSave()                                           // Save cache to disk
			fmt.Printf(" [ONLINE]")
		}
	}

	content = ACachedData.Content
	links = ACachedData.Links

	return
}

func getDomain(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		// log.Fatal(err)
		log.Println("getDomain error: ", err)
		return ""
	}

	// Find out main domain, ignore www. subdomains
	mainDomain := u.Hostname()
	var re = regexp.MustCompile(`^www\.(.*)$`)
	mainDomain = re.ReplaceAllString(mainDomain, `$1`)

	return mainDomain
}

func getSecondLevelDomain(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		// log.Fatal(err)
		log.Println("getSecondLevelDomain error: ", err)
		return ""
	}

	mainDomain := u.Hostname()
	var re = regexp.MustCompile(`^.*?([^\.]+\.[^\.]+)$`)
	mainDomain = re.ReplaceAllString(mainDomain, `$1`)

	// fmt.Printf("\n########## getSecondLevelDomain: %s", mainDomain)
	return mainDomain
}

func increaseDomainCounter(domain string) {
	domainCounter[domain]++
}

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

func linkSeemsOk(l string) bool {
	// fmt.Printf("\n\nlinkSeemsOk(%s)", l)
	if len(l) > 300 {
		return false
	}

	r, _ := regexp.Compile(regexLinkOk)
	if len(r.FindStringSubmatch(l)) > 0 {
		return true
	}
	return false
}

func getNextLink() (int, string) {
	// maxCount := 0
	maxi := 0
	lasti := 0
	maxUrl := ""
	// minDomainCounter := 0
	var priority, maxPriority float64
	for i, l := range LPool {
		// fmt.Printf("\n\ni,l = %d, %+v", i, l)
		priority = float64(l.Count) * float64(l.Count) / (float64(domainCounter[l.Domain]) + 1.0)

		if l.Status == 0 && priority > maxPriority && !isBanned(l.Url, l.Domain) && linkSeemsOk(l.Url) {
			// fmt.Printf("\n\nl.Count=%d, domainCounter[l.Domain]=%d, priority=%f, l.Url=%s", l.Count, domainCounter[l.Domain], priority, l.Url)

			// Set this item as best candidate so far
			maxi = i
			maxUrl = l.Url

			maxPriority = priority
		}
		lasti = i
	}
	fmt.Sprintf("* getNextLink() %d links on the pool. Found best link at %d position. Priority: %.03f\n", lasti, maxi, priority)

	increaseDomainCounter(LPool[maxi].Domain)

	return maxi, maxUrl
}

func addLink(link string, avoidFilters bool) {
	// fmt.Printf("\n\naddLink(%s, %+v)", link, avoidFilters)
	domain := getDomain(link)
	if !avoidFilters {
		// into canonical link by removing cgi parameters
		regex := `\?.*$`
		rs := regexp.MustCompile(regex)
		link = rs.ReplaceAllString(link, "")

		if domain == "" { // Avoid null and local urls
			return
		}

		if isBanned(link, domain) { // Avoid banned domains
			// fmt.Println("***** Banned domain: ", link)
			return
		}

		if !linkSeemsOk(link) { // Avoid links that do not pass keyword filter
			// fmt.Println("***** Link seems not ok: ", link)
			return
		}
	}

	// Full scan search for the link
	for i, l := range LPool {
		if l.Url == link {
			LPool[i].Count++
			return
		}
	}

	// Link is new
	LPool = append(LPool, ALink{Url: link, Domain: domain, Count: 1, Status: 0})
}

func linkBootstraping() {
	for _, l := range bootstrapingLinks {
		addLink(l, true)
	}

	myDebug("linkBootstraping", LPool)
}

func LPoolDump() {
	jdata, err := json.MarshalIndent(LPool, "", " ")
	if err != nil {
		fmt.Println("error: ", err)
	}
	// fmt.Println(string(jdata))
	jsonFile, err := os.Create("./LPool.json")
	jsonFile.Write(jdata)
}

func domainCounterDump() {
	jdata, err := json.MarshalIndent(domainCounter, "", " ")
	if err != nil {
		fmt.Println("domainCounterDump error: ", err)
	}
	// fmt.Println(string(jdata))
	jsonFile, err := os.Create("./domainCounter.json")
	jsonFile.Write(jdata)
}

func domainReportFailed(domain string) {
	f, err := os.OpenFile("./domainFailed.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer f.Close()
	if _, err := f.WriteString(domain + "\n"); err != nil {
		log.Println(err)
	}
}

func domainHadFailed(domain string) bool {
	file, err := os.Open("./domainFailed.log")
	if err != nil {
		return false
	}
	defer file.Close()
	b, err2 := ioutil.ReadAll(file)
	if err2 != nil {
		return false
	}

	re := regexp.MustCompile(`(?i)\W(` + domain + `)\W`)
	// fmt.Printf("\n\nregexp: %s", `(?i)\W(`+domain+`)\W`)
	matches := re.FindAllStringSubmatch(string(b[:]), -1)
	// fmt.Printf("\n\nmatches: %+v", matches)
	// fmt.Printf("\nlen(matches): %d", len(matches))
	if len(matches) > 6 {
		return true
	}

	return false
}

/***************************************************************************************************************
****************************************************************************************************************
* TOKENIZER ****************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// Tokenizer
// The tokenizer is the first step of text analysis. Its job is to convert text into a list of tokens. Our implementation splits the text on a word boundary and removes punctuation marks
func tokenize(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		// Split on any character that is not a letter or a number.
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func tokensCount(text string) int {
	return len(tokenize(text))
}

func splitParagraphs(text string) []string {
	re := regexp.MustCompile(`[\r\n]+`)
	ar := re.Split(text, -1)
	// myDebug(ar)

	return ar
}

func rankingByKeywords(text string) float64 {
	r, _ := regexp.Compile(regexRankingKeywords)
	rr := r.FindAllStringSubmatch(text, -1)

	logRanking.Printf("\nlen(r.FindAllStringSubmatch(text, -1)): %d", len(r.FindAllStringSubmatch(text, -1)))
	logRanking.Printf("\n1+len(tokenize(text)): %d", 1+len(tokenize(text)))
	logRanking.Printf("\nrr: %+v", rr)
	if len(text) > 1000 {
		logRanking.Printf("\n%s", text[:1000])
	}

	var uniqueK = make(map[string]int)

	for _, k := range rr {
		if len(k[1]) < 3 {
			continue
		}
		// logRanking.Printf("\nk: %+v", k)
		uniqueK[strings.ToLower(k[1])]++
	}

	logRanking.Printf("\nuniqueK: %+v", uniqueK)

	var ks []string
	for kk := range uniqueK {
		ks = append(ks, kk)
	}

	// return 100.0 * float64(len(r.FindAllStringSubmatch(text, -1))) / math.Sqrt(float64(1+len(tokenize(text))))
	rank := 100.0 * float64(len(ks)) / math.Sqrt(float64(1+len(tokenize(text))))

	logRanking.Printf("\nRank: %f", rank)

	return rank
}

func lowercaseFilter(tokens []string) []string {
	r := make([]string, len(tokens))
	for i, token := range tokens {
		r[i] = strings.ToLower(token)
	}
	return r
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func stopWordsCount(text string) int {
	regex := `(?i)\W(` + engStopWords + `)\W`
	// myDebug(regex)
	rs := regexp.MustCompile(regex)
	// t2 := rs.ReplaceAllString(text, " _ ")
	// myDebug(t2)
	matches := rs.FindAllStringIndex(text, -1)
	// myDebug(matches)

	return len(matches)
}

func stopwordFilter(text string) string {
	// fmt.Println("t1: ", text)

	// Words must be separated by 2 spaces to regexp find all stopwords
	doubleSpace := `(\W+)`
	r0 := regexp.MustCompile(doubleSpace)
	text = r0.ReplaceAllString(text, `  `)

	rs := regexp.MustCompile(regexStopwords)
	text = fmt.Sprintf(" %s ", text)
	text2 := rs.ReplaceAllString(text, ` `)
	text2 = strings.TrimSpace(text2)

	// fmt.Println("t2: ", text2)

	return text2
}

func stemmerFilter(tokens []string) []string {
	r := make([]string, len(tokens))
	for i, token := range tokens {
		r[i] = snowballeng.Stem(token, false)
	}
	return r
}

func analyze(t string) []string {
	tokens := tokenize(t)
	// fmt.Println(tokens)

	lTokens := lowercaseFilter(tokens)
	// fmt.Println(lTokens)

	nonstopTokens := tokenize(stopwordFilter(strings.Join(lTokens[:], " ")))
	// fmt.Println("\nnonstopTokens: ", nonstopTokens)

	// stemmedTokens := stemmerFilter(nonstopTokens)
	// fmt.Println(stemmedTokens)

	// return stemmedTokens
	return nonstopTokens
}

type freq map[string]int

var counter int

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

func getKVkeys(aMapArray []kv) []string {
	keys := make([]string, 0, len(aMapArray))
	for _, k := range aMapArray {
		keys = append(keys, k.Key)
	}

	return keys
}

/***************************************************************************************************************
****************************************************************************************************************
* MAIN *********************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// Logger
var (
	outfile, _  = os.Create("./download.log")
	logDownload = log.New(outfile, "", 0)
)

var (
	outfile2, _ = os.Create("./ranking.log")
	logRanking  = log.New(outfile2, "", 0)
)

var uniqueSignature = make(map[string]string)

var loopCount int
var writer *csv.Writer
var f freq
var bParagraphs = make(map[string]string)

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

func doNextLink() bool {
	maxi, nextLink := getNextLink()
	if nextLink == "" {
		fmt.Println("* No more links available in the pool")
		fmt.Println(maxi)
		return false // meaning there are no more links to explore
	}
	LPool[maxi].Status = 1
	fmt.Printf("\n* Downloading url: %s", nextLink)

	content, links, err := downloadCached(nextLink)
	// fmt.Printf("\ncontent, links, err := downloadCached(nextLink) => links = %+v", links)
	if err != nil {
		LPool[maxi].Status = 3
		fmt.Println("\nDownload error: ", err)
	} else {
		LPool[maxi].Status = 2
	}

	// Tokenizer url keywords frequencies
	// f.add(nextLink)
	// g := rSortFreq(f)
	// fmt.Println("\n\nTokenizer frequencies: ", g)

	// Split content into paragraphs
	paragraphs := splitParagraphs(content)
	// myDebug(paragraphs)

	// Remove urls, imgs, long words and low stopwords paragraphs from text
	for i, p := range paragraphs {
		// text = "* Commentary: China’s Ad5 vectored COVID-19 vaccine safe and induces immune response – News Medical ( https://www.news-medical.net/news/20200720/Chinas-Ad5-vectored-COVID-19-vaccine-safe-and-induces-immune-response.aspx ) AND CanSino COVID-19 vaccine generates immune response in 90% of patients – UPI ( https://www.upi.com/Health_News/2020/07/20/CanSino-COVID-19-vaccine-generates-immune-response-in-90-of-patients/5701595253844/ )"
		//p = "* Health ( )"
		// fmt.Printf("\n\n%s", p)
		regex1 := `(?i)\W([^ \t]*/[^ \t]*)\W`
		r1 := regexp.MustCompile(regex1)
		p2 := r1.ReplaceAllString(p, " ")
		//fmt.Printf("\n\n%s", p2)

		regex2 := `(?i)(<img[^>]+src=["'][^>]*["'][^>]*>)`
		r2 := regexp.MustCompile(regex2)
		p3 := r2.ReplaceAllString(p2, " ")
		// fmt.Printf("\n\n%s", p3)

		regex3 := `(?i)\W([^ \t\n]{80,})\W`
		r3 := regexp.MustCompile(regex3)
		p4 := r3.ReplaceAllString(p3, " ")
		// fmt.Printf("\n\n%s", paragraphs[i])

		numStopWords := stopWordsCount(p4)
		numTotalWords := len(tokenize(p4))
		ratioStopWords := float64(numStopWords) / float64(numTotalWords+1)
		if ratioStopWords < 0.1 {
			paragraphs[i] = ""
		} else {
			paragraphs[i] = p4
		}
		//fmt.Printf("\n\n%s", paragraphs[i])
		//panic(1)
	}

	bParagraphs[nextLink] = bestParagraph(paragraphs)

	curatedContent := ""
	for _, p := range paragraphs {
		if len(p) < 200 {
			continue
		}
		ratio := float64(stopWordsCount(p)) / float64(tokensCount(p)+1)
		if ratio < 0.1 || ratio > 0.38 {
			// if ratio < 0.1 {
			// 	fmt.Printf("\nSMALL ratio: %.03f %d %d %d paragrap: %s", ratio, len(p), stopWordsCount(p), tokensCount(p), p)
			// }
			// if ratio > 0.38 {
			// 	fmt.Printf("\nBIG ratio: %.03f %d %d %d paragrap: %s", ratio, len(p), stopWordsCount(p), tokensCount(p), p)
			// }
			continue
		}
		curatedContent = curatedContent + "\n" + p
	}

	// fmt.Printf("\n\n%s", curatedContent)

	// Doc length
	docLen := len(tokenize(curatedContent))

	// Cut values on numWords 1k - 10k
	if docLen > 100000 || docLen < 200 {
		// if docLen < 200 {
		// 	fmt.Printf("\n*** docLen < 1000 : %d %s", docLen, curatedContent)
		// }
		if docLen > 100000 {
			fmt.Printf("\n*** docLen > 100000 : %d %s", docLen, curatedContent[:1000])
		}
		return true
	}

	// Current doc frequencies
	fDoc := make(freq)
	fDoc.add(curatedContent)
	gDoc := rSortFreq(fDoc)
	// if nextLink == "https://www.england.nhs.uk/statistics/statistical-work-areas/covid-19-daily-deaths/" {
	// 	fmt.Println("\n\nDoc frequencies: ", gDoc[:1])
	// 	fmt.Println("\n\nDoc numWords: ", docLen)
	// 	fmt.Printf("\n\nDoc maxFreq/numWords ratio: %.03f", float64(gDoc[0].Value)/float64(1+docLen))
	// 	// panic(1)
	// }

	// fmt.Printf("\n\n%v", fDoc)
	gDocSignature := ""
	if len(gDoc) > 7 {
		gDocSignature = fmt.Sprintf("%v", getKVkeys(gDoc[:7]))
	} else {
		gDocSignature = fmt.Sprintf("%v", getKVkeys(gDoc))
	}
	// panic(gDocSignature)

	// ignore when signature is not unique
	if uniqueSignature[gDocSignature] == "" {
		uniqueSignature[gDocSignature] = nextLink
	} else {
		fmt.Printf("\n\n++++++++++ SIMILAR FOUND ON %s\n", uniqueSignature[gDocSignature])
		return true
	}

	// err = writer.Write([]string{gDoc[0].Key, fmt.Sprintf("%d", gDoc[0].Value), fmt.Sprintf("%d", docLen), fmt.Sprintf("%.03f", float64(gDoc[0].Value)/float64(1+docLen)), nextLink})
	err = writer.Write([]string{fmt.Sprintf("%.02f", rankingByKeywords(curatedContent)), fmt.Sprintf("%d", docLen), gDocSignature, nextLink, bParagraphs[nextLink]})

	if err != nil {
		log.Fatal("Cannot write to file", err)
	}
	writer.Flush()

	// Cut values on maxFreq/numWords ratio 0.1 - 0.005
	if float64(gDoc[0].Value)/float64(1+docLen) > 0.1 || float64(gDoc[0].Value)/float64(1+docLen) < 0.005 {
		fmt.Printf("\n*** Filter on maxFreq/numWords ratio : %.03f %s", float64(gDoc[0].Value)/float64(1+docLen), curatedContent)
		return true
	}

	// Tokenizer text token frequencies
	f.add(curatedContent)

	// Once in a while...
	loopCount++
	if loopCount%10 == 0 {
		g := rSortFreq(f)
		fmt.Println("\n\nCorpus frequencies: ", g[:100])

		// LPool dump to file
		LPoolDump()
		domainCounterDump()
	}

	// push content into persitent ddbb
	// save(curatedContent, l.Url)

	// add links to pool
	for _, link := range links {
		// fmt.Printf("\nfor _, link := range links ... =>  %s", link)
		if strings.Contains(getDomain(link), getSecondLevelDomain(nextLink)) {
			// fmt.Printf("\nSite link ignored: %s", link)
		} else {
			addLink(link, false)
			if strings.Contains(link, "wikipedia") {
				fmt.Printf("\n*************************************** %s [%s:%s]", link, getDomain(link), getSecondLevelDomain(nextLink))
			}
		}
	}
	fmt.Printf(" %d links found", len(links))

	return true
}

func main() {
	// Allow go interfaces be expanded into custom structs of our cache implementation
	gob.Register(CachedData{}) // For some reason, this declaration must be written on main function

	fmt.Println("* Init cache ...")
	cacheInit()

	fmt.Println("* Link bootstrapping ...")
	linkBootstraping()

	// Tokenizer frequencies
	f = make(freq)
	// var dataCSV = [][]string{{}}
	// file, err := os.Create("maxFreq-numWords-URL.csv")
	file, err := os.Create("ranking-URL.csv")
	if err != nil {
		log.Fatal("Cannot create file", err)
	}
	defer file.Close()

	writer = csv.NewWriter(file)
	writer.Comma = '\t'
	// defer writer.Flush()

	// Testing a single url download
	// urlLink := ""
	// _, _, err = download(urlLink)
	// panic(err)

	for {
		if !doNextLink() {
			break
		}
	}
	fmt.Println("***** Done!!!")
}
