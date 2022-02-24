// aggregates coronavirus related research documents
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackdanger/collectlinks"
	snowballeng "github.com/kljensen/snowball/english"
	"github.com/patrickmn/go-cache"
	"github.com/spf13/viper"
	"jaytaylor.com/html2text"

	"github.com/abadojack/whatlanggo"

	"github.com/computerphysicslab/goPackages/goDebug"

	"goCrawler/corpusfreqlib"
	"goCrawler/iolib"
	"goCrawler/redislib"
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
var bootstrapingLinks []string
var minDocLen, maxDocLen int
var scoreThreshold float64

/***************************************************************************************************************
****************************************************************************************************************
* CSV FUNCTIONS ************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

var csvWriter *csv.Writer

func csvInit() {
	// var dataCSV = [][]string{{}}
	// file, err := os.Create("maxFreq-numWords-URL.csv")
	file, err := os.Create("ranking-URL.csv")
	if err != nil {
		log.Fatal("Cannot create file: ", err)
	}
	// defer file.Close()

	csvWriter = csv.NewWriter(file)
	csvWriter.Comma = '\t'
	// defer csvWriter.Flush()
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

// A cache to avoid repeated HTTP calls from crawlers
var myCache *cache.Cache

func cacheInit() {
	// Load serialized cache from file if exists
	b, err := ioutil.ReadFile("./cache/cachePersistent.dat")
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

var saveBackupCount int // to static...

// Store cache into persistent file
func cacheSave() {
	// save a backup from time to time
	saveBackupCount++
	if saveBackupCount%10 == 0 {
		err := iolib.CopyFileContents("./cache/cachePersistent.dat", "./cache/cachePersistent.backup")
		if err != nil {
			panic(err)
		}
		fmt.Printf("\n\n##################################### BACKUP ##########################################\n")
	}

	// Serialize cache
	b := new(bytes.Buffer)
	e := gob.NewEncoder(b)
	// Encoding the map
	err := e.Encode(myCache.Items())
	// goDebug.Print(myCache.Items())
	if err != nil {
		panic(err)
	}

	// Save serialized cache into file
	err = ioutil.WriteFile("./cache/cachePersistent.dat", b.Bytes(), 0644)
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
	// b, found := myCache.Get(urlLink)
	// Get results from Redis cache if available
	found := redislib.Exists("cache_" + urlLink)
	if found {
		// b := redislib.GetInterface("cache_" + urlLink)
		// ACachedData = b.(CachedData)
		err = json.Unmarshal([]byte(redislib.Get("cache_"+urlLink)), &ACachedData)
		if err != nil {
			panic(err)
		}

		fmt.Printf(" [CACHE]")
		// fmt.Println("ACachedData: ", ACachedData)
		err = nil
	} else {
		ACachedData.Content, ACachedData.Links, err = download(urlLink)
		if err == nil {
			// myCache.Set(urlLink, ACachedData, cache.NoExpiration) // Store download results in cache
			redislib.SetInterface("cache_"+urlLink, ACachedData) // Store download results in Redis cache
			// cacheSave()                                           // Save cache to disk
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
	if len(l) > 300 {
		return false
	}

	r, _ := regexp.Compile(regexLinkOk)
	if len(r.FindStringSubmatch(l)) > 0 {
		return true
		// } else {
		// fmt.Printf("\n\nlinkSeemsOk(%s) failed to match regexLinkOk: %s", l, regexLinkOk)
	}

	return false
}

func getNextLink() (int, string) {
	maxi := 0
	lasti := 0
	maxURL := ""
	var priority, maxPriority float64

	// fmt.Printf("* getNextLink() %d links on the pool\n", len(lPool))

	for i, l := range lPool {
		if l.Status == 4 { // bootstrapping url
			fmt.Printf("\n\nFound bootstrapping url: %+v", l)
			// Set this item directly as next candidate
			maxi = i
			maxURL = l.URL
			maxPriority = 0
			break
		}

		// fmt.Printf("\n\ni,l = %d, %+v", i, l)
		priority = float64(l.Count) * float64(l.Count) / (float64(domainCounter[l.Domain]) + 1.0)

		if l.Status == 0 && priority > maxPriority && !isBanned(l.URL, l.Domain) && linkSeemsOk(l.URL) {
			// fmt.Printf("\n\nl.Count=%d, domainCounter[l.Domain]=%d, priority=%f, l.URL=%s", l.Count, domainCounter[l.Domain], priority, l.URL)

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

func addLink(link string, avoidFilters bool) bool {
	// fmt.Printf("\n\naddLink(%s, %+v)", link, avoidFilters)
	domain := getDomain(link)
	if !avoidFilters {
		if domain == "" { // Avoid null and local urls
			return false
		}

		if isBanned(link, domain) { // Avoid banned domains
			// fmt.Println("***** Banned domain: ", link)
			return false
		}

		if !linkSeemsOk(link) { // Avoid links that do not pass keyword filter
			// fmt.Println("***** Link seems not ok: ", link)
			return false
		}

		// into canonical link by removing cgi parameters
		regex := `\?.*$`
		rs := regexp.MustCompile(regex)
		link2 := rs.ReplaceAllString(link, "")
		if link2 != link {
			logCGI.Printf("\n\n%s\n%s", link2, link)
			link = link2
		}
	}

	// Full scan search for the link
	for i, l := range lPool {
		if l.URL == link {
			lPool[i].Count++
			return true
		}
	}

	// Link is new
	if avoidFilters {
		lPool = append(lPool, aLink{URL: link, Domain: domain, Count: 1, Status: 4})
	} else {
		lPool = append(lPool, aLink{URL: link, Domain: domain, Count: 1, Status: 0})
	}

	return true
}

func linkBootstraping() {
	for _, l := range bootstrapingLinks {
		addLink(l, true)
	}

	goDebug.Print("linkBootstraping", lPool)
}

func lPoolDump() {
	jdata, err := json.MarshalIndent(lPool, "", " ")
	if err != nil {
		fmt.Println("error: ", err)
	}
	// fmt.Println(string(jdata))
	jsonFile, err := os.Create("./LPool.json")
	if err != nil {
		log.Println(err)
	}
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
	iolib.String2fileAppend(domain+"\n", "./logs/domainFailed.log")
}

func domainHadFailed(domain string) bool {
	re := regexp.MustCompile(`(?i)\W(` + domain + `)\W`)
	// fmt.Printf("\n\nregexp: %s", `(?i)\W(`+domain+`)\W`)
	matches := re.FindAllStringSubmatch(iolib.File2string("./logs/domainFailed.log"), -1)
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
	// goDebug.Print(ar)

	return ar
}

func rankingByKeywords(text string) float64 {
	r, _ := regexp.Compile(regexRankingKeywords)
	rr := r.FindAllStringSubmatch(text, -1)

	// logRanking.Printf("\nlen(r.FindAllStringSubmatch(text, -1)): %d", len(r.FindAllStringSubmatch(text, -1)))
	// logRanking.Printf("\n1+len(tokenize(text)): %d", 1+len(tokenize(text)))
	// logRanking.Printf("\nrr: %+v", rr)
	// if len(text) > 1000 {
	// 	logRanking.Printf("\n%s", text[:1000])
	// }

	var uniqueK = make(map[string]int)

	for _, k := range rr {
		if len(k[1]) < 3 {
			continue
		}
		// logRanking.Printf("\nk: %+v", k)
		uniqueK[strings.ToLower(k[1])]++
	}

	// logRanking.Printf("\nuniqueK: %+v", uniqueK)

	var ks []string
	for kk := range uniqueK {
		ks = append(ks, kk)
	}

	// return 100.0 * float64(len(r.FindAllStringSubmatch(text, -1))) / math.Sqrt(float64(1+len(tokenize(text))))
	rank := 100.0 * float64(len(ks)) / math.Sqrt(float64(1+len(tokenize(text))))

	// logRanking.Printf("\nRank: %f", rank)

	return rank
}

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

func lowercaseFilter(tokens []string) []string {
	r := make([]string, len(tokens))
	for i, token := range tokens {
		r[i] = strings.ToLower(token)
	}
	return r
}

func stopWordsCount(text string) int {
	regex := `(?i)\W(` + engStopWords + `)\W`
	// goDebug.Print(regex)
	rs := regexp.MustCompile(regex)
	// t2 := rs.ReplaceAllString(text, " _ ")
	// goDebug.Print(t2)
	matches := rs.FindAllStringSubmatch(" "+text+" ", -1)
	// goDebug.Print(matches)

	return len(matches)
}

func stopWordsOnBorderCount(text string) int {
	regexLeft := `(?i)^(` + engStopWords + `)\W`
	rsLeft := regexp.MustCompile(regexLeft)
	matchesLeft := rsLeft.FindAllStringIndex(text, -1)

	regexRight := `(?i)\W(` + engStopWords + `)$`
	rsRight := regexp.MustCompile(regexRight)
	matchesRight := rsRight.FindAllStringIndex(text, -1)

	return len(matchesLeft) + len(matchesRight)
}

func lowRelevancyWordsOnBorderCount(text string) int {
	regexLeft := `(?i)^(` + engStopWords + engLowRelevancyWords + `)\W`
	rsLeft := regexp.MustCompile(regexLeft)
	matchesLeft := rsLeft.FindAllStringIndex(text, -1)

	regexRight := `(?i)\W(` + engStopWords + `)$`
	rsRight := regexp.MustCompile(regexRight)
	matchesRight := rsRight.FindAllStringIndex(text, -1)

	return len(matchesLeft) + len(matchesRight)
}

// Remove stopwords
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

// func bigramsOf(t string) []string {
// 	// var unigrams = []string{}
// 	var bigrams = []string{}
// 	var trigrams = []string{}

// 	// Regard each sentence as a paragraph
// 	t = strings.ReplaceAll(t, ".\n", "\n")
// 	t = strings.ReplaceAll(t, ". ", "\n")
// 	paragraphs := splitParagraphs(t)

// 	// Ignore repeated/similar sentences
// 	var uniqueSentenceSignatures = make(map[string]string)
// 	for _, p := range paragraphs {
// 		// remove stopwords from sentence
// 		p2 := stopwordFilter(p)

// 		// Find high frequency meaningful tokens
// 		sentenceTokenFreqs := make(freq)
// 		sentenceTokenFreqs.add(p2)
// 		sentenceTokenFreqsSorted := rSortFreq(sentenceTokenFreqs)

// 		// Build sentence signature w/ highest frequency meaningful tokens
// 		sentenceSignature := ""
// 		if len(sentenceTokenFreqsSorted) > 7 {
// 			sentenceSignature = fmt.Sprintf("%v", getKVkeys(sentenceTokenFreqsSorted[:7]))
// 		} else {
// 			sentenceSignature = fmt.Sprintf("%v", getKVkeys(sentenceTokenFreqsSorted))
// 		}

// 		// Useful sentences are stored with no duplication on uniqueSentenceSignatures value field
// 		if uniqueSentenceSignatures[sentenceSignature] == "" {
// 			uniqueSentenceSignatures[sentenceSignature] = p
// 			// fmt.Printf("uniqueSentenceSignatures: + %s\n", p)
// 		} else {
// 			// fmt.Printf("uniqueSentenceSignatures: - %s\n", p)
// 		}
// 	}

// 	// Words must be separated by 2 spaces to regexp find all matches
// 	r0 := regexp.MustCompile(`(\W+)`)

// 	// Avoid meaningless characters
// 	r1 := regexp.MustCompile(`([*\(\)\?\-\,\:\#\[\]\"\“]+)`)

// 	// Remove double spaces
// 	rf := regexp.MustCompile(`(\W)\W`)

// 	// Find bigrams
// 	re := regexp.MustCompile(`(?i)\W([^\W]+)\W`)

// 	for _, p := range uniqueSentenceSignatures {
// 		// Prepare text
// 		p = r0.ReplaceAllString(" "+p+" ", `$1$1`)
// 		p = r1.ReplaceAllString(p, ` `)

// 		// Find bigrams
// 		matches := re.FindAllStringSubmatch(p, -1)

// 		previousToken := ""
// 		previousToken2 := ""
// 		for _, m := range matches {
// 			// unigrams = append(unigrams, m[1])
// 			// if previousToken2 != "" {
// 			if len(tokenize(previousToken2)) == 2 {
// 				trigramCandidate := strings.TrimSpace(previousToken2 + m[0])

// 				// Remove double spaces
// 				trigramCandidate = rf.ReplaceAllString(trigramCandidate, `$1`)

// 				// avoid trigrams containing stopwords
// 				if stopWordsCount(trigramCandidate) == 0 {
// 					trigrams = append(trigrams, trigramCandidate)
// 				}
// 				// fmt.Println("T: ", trigramCandidate, stopWordsCount(trigramCandidate))
// 			}
// 			if previousToken != "" {
// 				bigramCandidate := strings.TrimSpace(previousToken + m[0])

// 				// Remove double spaces
// 				bigramCandidate = rf.ReplaceAllString(bigramCandidate, `$1`)

// 				// avoid bigrams containing stopwords
// 				if stopWordsCount(bigramCandidate) == 0 {
// 					bigrams = append(bigrams, bigramCandidate)
// 				}
// 				// fmt.Println("B: ", bigramCandidate, stopWordsCount(bigramCandidate))
// 			}
// 			// fmt.Printf("previousToken2: #%s#\n", previousToken2)
// 			// fmt.Printf("previousToken: #%s#\n", previousToken)
// 			previousToken2 = previousToken + m[0]
// 			// if len(tokenize(previousToken2)) != 2 {
// 			// 	//fmt.Println("T: ", trigramCandidate)
// 			// 	fmt.Println("previousToken2: ", previousToken2)
// 			// 	fmt.Println("previousToken: ", previousToken)
// 			// 	fmt.Println("m[0]: ", m[0])
// 			// }

// 			previousToken = m[0]
// 			// fmt.Printf("previousToken2: #%s#\n", previousToken2)
// 			// fmt.Printf("previousToken: #%s#\n", previousToken)
// 		}
// 	}
// 	return trigrams
// }

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
			// fmt.Printf("uniqueSentenceSignatures: + %s\n", p)
		} else {
			// fmt.Printf("uniqueSentenceSignatures: - %s\n", p)
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
				// fmt.Println("ngramCandidate, stopWordsCount: ", ngramCandidate, stopWordsCount(ngramCandidate))
			}

			for i := n - 1; i > 0; i-- {
				previousToken[i] = previousToken[i-1] + aBigramFound[0]
			}
			previousToken[0] = aBigramFound[0]
		}
	}
	return ngrams
}

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
		// fmt.Println("N-GRAM: ", anNgramFreq.Key, anNgramFreq.Value)
	}

	return limitedNgramsFreqSorted
}

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

// Logger
var (
	outfile, _  = os.Create("./logs/download.log")
	logDownload = log.New(outfile, "", 0)
)

// var (
// 	outfile2, _ = os.Create("./logs/ranking.log")
// 	logRanking  = log.New(outfile2, "", 0)
// )

var (
	outfile3, _ = os.Create("./logs/cgi.log")
	logCGI      = log.New(outfile3, "", 0)
)

var (
	outfile4, _ = os.Create("./logs/domainFailed.log")
	logDomainFailed = log.New(outfile4, "", 0)
)

var uniqueSignature = make(map[string]string)
var corpusFreqs freq = make(freq)

func addLinksOf(nextLink string, links []string) {
	linksAdded := 0
	// add links to pool
	for _, link := range links {
		// fmt.Printf("\nfor _, link := range links ... =>  %s", link)
		if strings.Contains(getDomain(link), getSecondLevelDomain(nextLink)) {
			// fmt.Printf("\nSite link ignored: %s", link)
		} else {
			if addLink(link, false) {
				linksAdded++
			}
			if strings.Contains(link, "wikipedia") {
				fmt.Printf("\n*************************************** %s [%s:%s]", link, getDomain(link), getSecondLevelDomain(nextLink))
			}
		}
	}
	fmt.Printf(" %d links found (%d added)", len(links), linksAdded)
}

func doNextLink(numLinksProcessed int) bool {
	maxi, nextLink := getNextLink()
	if nextLink == "" {
		fmt.Println("* No more links available in the pool")
		fmt.Println(maxi)
		return false // meaning there are no more links to explore
	}
	prevState := lPool[maxi].Status
	lPool[maxi].Status = 1
	fmt.Printf("\n* Downloading url: %s", nextLink)

	content, links, err := downloadCached(nextLink)
	// fmt.Printf("\ncontent, links, err := downloadCached(nextLink) => links = %+v", links)
	if err != nil {
		lPool[maxi].Status = 3
		fmt.Println("\nDownload error: ", err)
	} else {
		lPool[maxi].Status = 2
	}

	// Adding links of bootstrapping before filters
	if prevState == 4 && lPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}

	// Tokenizer url keywords frequencies
	// f.add(nextLink)
	// g := rSortFreq(f)
	// fmt.Println("\n\nTokenizer frequencies: ", g)

	// Split content into paragraphs
	paragraphs := splitParagraphs(content)
	// goDebug.Print(paragraphs)

	// Remove urls, imgs, long words and low stopwords paragraphs from text
	for i, p := range paragraphs {
		// Language detection

		// regex0 := `(?i)([ñçüịủĐứđượđềộềậệụạă])` // To avoid processing international characters that behave as a word separator, like "ñ"
		// r0 := regexp.MustCompile(regex0)
		// matches := r0.FindAllStringSubmatch(p, -1)
		// if len(matches) > 0 {

		// if strings.ContainsAny(p, `ñçüịủĐứđượđềộềậệụạăšýěůčžČủữăòốêầ`) { // To avoid processing international characters that behave as a word separator, like "ñ"
		// 	paragraphs[i] = ""
		// 	continue
		// }

		// if !isEnglish(p) {
		// 	paragraphs[i] = ""
		// 	fmt.Printf("\n\nNOT ENGLISH: %s", p)
		// 	continue
		// }

		whatlanggoInfo := whatlanggo.Detect(p)
		if whatlanggoInfo.Lang.String() != "English" {
			paragraphs[i] = ""
			// fmt.Printf("\n\nNOT ENGLISH (%s): %s", whatlanggoInfo.Lang.String(), p)
			continue
		}

		regex1 := `(?i)\W([^ \t]*/[^ \t]*)\W`
		r1 := regexp.MustCompile(regex1)
		p2 := r1.ReplaceAllString(p, " ")
		//fmt.Printf("\n\n%s", p2)

		regex2 := `(?i)(<(p|img|div)[^>]*>)`
		r2 := regexp.MustCompile(regex2)
		p3 := r2.ReplaceAllString(p2, " ")
		if p2 != p3 {
			// fmt.Printf("\n\n***** p2 != p3: \n%s\n%s\n", p2, p3)
		}

		regex3 := `(?i)\W([^ \t\n]{80,})\W`
		r3 := regexp.MustCompile(regex3)
		p4 := r3.ReplaceAllString(p3, " ")
		// fmt.Printf("\n\n%s", p4)

		regex4 := `(?i)\W(div|img|nofollow|javascript:|(alt|class|style|width|height|onclick)="[^"]*")\W`
		r4 := regexp.MustCompile(regex4)
		// p5 := r4.ReplaceAllString(p4, " ")
		matches4 := r4.FindAllStringSubmatch(p, -1)
		// if p4 != p5 {
		// 	// fmt.Printf("\n\n***** p4 != p5: \n%s\n%s\n", p4, p5)
		// }
		if len(matches4) > 0 {
			paragraphs[i] = ""
			continue
		}
		p5 := p4

		numStopWords := stopWordsCount(p5)
		numTotalWords := len(tokenize(p5))
		ratioStopWords := float64(numStopWords) / float64(numTotalWords+1)
		// fmt.Printf("\n\nratioStopWords: %f: %s", ratioStopWords, paragraphs[i])
		if ratioStopWords < 0.1 {
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
		// fmt.Printf("\nratio: %.03f %d %d %d paragraph: %s", ratio, len(p), stopWordsCount(p), tokensCount(p), p)
		if ratio < 0.1 || ratio > 0.38 {
			// if ratio < 0.1 {
			// 	fmt.Printf("\nSMALL ratio: %.03f %d %d %d paragraph: %s", ratio, len(p), stopWordsCount(p), tokensCount(p), p)
			// }
			// if ratio > 0.38 {
			// 	fmt.Printf("\nBIG ratio: %.03f %d %d %d paragraph: %s", ratio, len(p), stopWordsCount(p), tokensCount(p), p)
			// }
			continue
		}
		curatedContent = curatedContent + "\n" + p
	}

	// Doc length
	docLen := len(tokenize(curatedContent))

	// Cut filter on curated numWords
	if docLen > maxDocLen {
		fmt.Printf("\n*** docLen > %d : %d %s", maxDocLen, docLen, curatedContent[:1000])
		return true
	}

	if docLen < minDocLen {
		fmt.Printf("\n*** docLen < %d : %d %s", minDocLen, docLen, curatedContent)
		return true
	}

	iolib.String2fileAppend(nextLink+"\n"+curatedContent+"----\n\n\n\n", "./logs/corpusCuratedText.log")
	// fmt.Printf("\n\ncuratedContent: %s", curatedContent)

	// Current doc frequencies
	fDoc := make(freq)
	fDoc.add(curatedContent)
	// remove "the" frequency
	fDoc["the"] = 0
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

	// Ignore when signature is not unique
	if uniqueSignature[gDocSignature] == "" {
		uniqueSignature[gDocSignature] = nextLink
	} else {
		fmt.Printf("\n\n++++++++++ SIMILAR FOUND ON %s\n", uniqueSignature[gDocSignature])
		return true
	}

	// Ignore when ranking score is lower than cutoff threshold
	s := rankingByKeywords(curatedContent)
	if s < scoreThreshold {
		fmt.Printf("\n\n++++++++++ NOT ENOUGH RELEVANCY %+v\n", s)
		return true
	}

	// Append CSV row
	// err = csvWriter.Write([]string{gDoc[0].Key, fmt.Sprintf("%d", gDoc[0].Value), fmt.Sprintf("%d", docLen), fmt.Sprintf("%.03f", float64(gDoc[0].Value)/float64(1+docLen)), nextLink})
	err = csvWriter.Write([]string{fmt.Sprintf("%.02f", rankingByKeywords(curatedContent)), fmt.Sprintf("%d", docLen), gDocSignature, nextLink, bParagraph})
	if err != nil {
		log.Fatal("Cannot write to file", err)
	}
	csvWriter.Flush()

	// Cut values on maxFreq/numWords ratio 0.1 - 0.002
	if float64(gDoc[0].Value)/float64(1+docLen) > 0.1 || float64(gDoc[0].Value)/float64(1+docLen) < 0.002 {
		fmt.Printf("\n*** Filter on maxFreq/numWords ratio : %.03f %s", float64(gDoc[0].Value)/float64(1+docLen), curatedContent)
		return true
	}

	// Tokenizer text token frequencies
	corpusFreqs.add(curatedContent)

	// Once in a while...
	if numLinksProcessed%50 == 0 {
		fmt.Printf("\n\nnumLinksProcessed: %d", numLinksProcessed)

		corpusFreqsSorted := rSortFreq(corpusFreqs)
		fmt.Println("\n\nCorpus frequencies: ", corpusFreqsSorted[:100])

		// Saving corpus frequencies in format all.num from British National Corpus
		output := ""
		for _, gg := range corpusFreqsSorted {
			output = output + fmt.Sprintf("%d %s %s %d\n", gg.Value, gg.Key, "none", 0)
		}
		iolib.String2file(output, "./corpusFrequencies.txt")

		// substracting english words frequencies
		corpusFreqsWithoutEnglish := make(freq) // specific corpus token frequencies w/o english baseline

		var intercorpusScaleFactor float64
		intercorpusContrast := 20.0
		if corpusFreqsSorted[0].Key == "the" {
			intercorpusScaleFactor = float64(1+corpusfreqlib.Freq("the")) / float64(corpusFreqsSorted[0].Value)
		} else {
			panic("Error: stopword \"the\" not found!")
		}

		// keyValue={Key:the Value:5498} [eng: 6187267] [corpusFreqsWithoutEnglish: 0]
		// keyValue={Key:covid Value:1867} [eng: 0] [corpusFreqsWithoutEnglish: 1867]
		// keyValue={Key:patients Value:1247} [eng: 17330] [corpusFreqsWithoutEnglish: 0]
		// keyValue={Key:fda Value:604} [eng: 23] [corpusFreqsWithoutEnglish: 25]
		// keyValue={Key:disease Value:504} [eng: 8905] [corpusFreqsWithoutEnglish: 0]
		// keyValue={Key:test Value:436} [eng: 9663] [corpusFreqsWithoutEnglish: 0]
		// keyValue={Key:sars Value:430} [eng: 17] [corpusFreqsWithoutEnglish: 23]
		// keyValue={Key:cov Value:389} [eng: 30] [corpusFreqsWithoutEnglish: 12]
		// keyValue={Key:tests Value:388} [eng: 4681] [corpusFreqsWithoutEnglish: 0]
		for _, keyValue := range corpusFreqsSorted {
			// By division:
			// corpusFreqsWithoutEnglish[keyValue.Key] = int(intercorpusScaleFactor * float64(keyValue.Value) / float64(1+corpusfreqlib.Freq(keyValue.Key)))
			// By substraction:
			corpusFreqsWithoutEnglish[keyValue.Key] = keyValue.Value - int(intercorpusContrast*float64(1+corpusfreqlib.Freq(keyValue.Key))/intercorpusScaleFactor)
			// fmt.Printf("\nkeyValue=%+v [eng: %d] [corpusFreqsWithoutEnglish: %d]", keyValue, corpusfreqlib.Freq(keyValue.Key), corpusFreqsWithoutEnglish[keyValue.Key])
		}
		corpusFreqsWithoutEnglishSorted := rSortFreq(corpusFreqsWithoutEnglish)
		fmt.Println("\n\nCorpus frequencies w/o Eng.: ", corpusFreqsWithoutEnglishSorted[:100])

		// Saving corpus w/o English frequencies in basic format
		output = ""
		for _, gg := range corpusFreqsWithoutEnglishSorted {
			output = output + fmt.Sprintf("%d %s\n", gg.Value, gg.Key)
		}
		iolib.String2file(output, "./corpusNoEngFrequencies.txt")

		// LPool dump to file
		lPoolDump()
		domainCounterDump()

		// // Entities for global curated corpus
		// corpusCuratedText := iolib.File2string("./logs/corpusCuratedText.log")
		// doc, _ := prose.NewDocument(corpusCuratedText)
		// entityFreq := make(freq)
		// for _, ent := range doc.Entities() {
		// 	entityFreq[ent.Text+" :: "+ent.Label]++
		// 	// fmt.Println(ent.Text, ent.Label)
		// }
		// entityFreqSorted := rSortFreq(entityFreq)
		// for counterEntityFreq, anEntityFreq := range entityFreqSorted {
		// 	fmt.Println(anEntityFreq.Key, anEntityFreq.Value)
		// 	if counterEntityFreq > 30 {
		// 		break
		// 	}
		// }

		// Entities/Bigrams for global curated corpus
		// corpusCuratedText := iolib.File2string("./logs/corpusCuratedText.log")
		// // ngramsOf
		// corpusCuratedBigrams := bigramsOf(corpusCuratedText)
		// entityFreq := make(freq)
		// for _, ent := range corpusCuratedBigrams {
		// 	entityFreq[ent]++
		// }
		// entityFreqSorted := rSortFreq(entityFreq)
		// for counterEntityFreq, anEntityFreq := range entityFreqSorted {
		// 	fmt.Println("BIGRAM: ", anEntityFreq.Key, anEntityFreq.Value)
		// 	if counterEntityFreq > 100 {
		// 		break
		// 	}
		// }
	}

	// push content into persitent ddbb
	// save(curatedContent, l.URL)

	// Adding links of urls passing filters
	if prevState == 0 && lPool[maxi].Status == 2 {
		addLinksOf(nextLink, links)
	}

	return true
}

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

	fmt.Printf("\n\nregexBannedDomains: %s", regexBannedDomains)
	fmt.Printf("\n\nregexLinkBannedTokens: %s", regexLinkBannedTokens)
	fmt.Printf("\n\nengStopWordsWOthe: %s", engStopWordsWOthe)
	fmt.Printf("\n\nengStopWords: %s", engStopWords)
	fmt.Printf("\n\nengLowRelevancyWords: %s", engLowRelevancyWords)
	fmt.Printf("\n\nregexStopwords: %s", regexStopwords)
	fmt.Printf("\n\ndownloadTimeout: %+v", downloadTimeout)
}

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
	// fmt.Printf("\n\nproxyPass: %s", proxyPass)
}

func yamlInitSpecific() {
	argsWithoutProg := os.Args[1:]
	viper.SetConfigName(argsWithoutProg[0]) // name of config file (without extension)
	viper.AddConfigPath(".")                // look for config in the working directory
	err := viper.ReadInConfig()             // Find and read the config file
	if err != nil {                         // Handle errors reading the config file
		panic(fmt.Errorf("Fatal error config file: %s", err))
	}
	curatedDomains = stringlib.RmNewLines(viper.GetString("curatedDomains"))
	regexLinkOk = `(?i)^https*://.*(` + stringlib.RmNewLines(viper.GetString("linkOk")) + `|` + curatedDomains + `)`
	regexRankingKeywords = stringlib.RmNewLines(viper.GetString("regexRankingKeywords"))
	bootstrapingLinks = viper.GetStringSlice("bootstrapingLinks")
	minDocLen = viper.GetInt("minDocLen")
	maxDocLen = viper.GetInt("maxDocLen")
	scoreThreshold = float64(viper.GetInt("scoreThreshold"))

	fmt.Printf("\n\nargsWithoutProg: %+v", argsWithoutProg)
	fmt.Printf("\n\ncuratedDomains: %s", curatedDomains)
	fmt.Printf("\n\nregexLinkOk: %s", regexLinkOk)
	fmt.Printf("\n\nregexRankingKeywords: %s", regexRankingKeywords)
	fmt.Printf("\n\nbootstrapingLinks: %+v", bootstrapingLinks)
}

func main() {
	// TODO: create logs folder if not exist
	fmt.Println("* Checking logs folder ...")
	if _, err := os.Stat("./logs"); os.IsNotExist(err) {
		// Logs folder does not exist, we create it automatically
		os.MkdirAll("./logs", os.ModePerm)
	}

	// TODO: check redislib is working ok
	fmt.Println("* Checking Redis service ...")
	err := redislib.Ping()
	if err != nil {
		fmt.Println("Redis service reverts error: ", err)
	}

	fmt.Println("* Loading YAML config ...")
	yamlInitGeneral()
	yamlInitProxy()
	yamlInitSpecific()

	fmt.Println("* Init English corpus ...")
	corpusfreqlib.Init()

	// Ngrams for global curated corpus
	if false {
		fmt.Printf("\n")
		// corpusCuratedText := iolib.File2string("./logs/corpusCuratedText-Covid19-small.txt")
		// corpusCuratedText := iolib.File2string("./logs/corpusCuratedText-Covid19.txt")
		corpusCuratedText := iolib.File2string("./logs/corpusCuratedText-Covid19-medium.txt")
		// corpusCuratedText := iolib.File2string("./logs/corpusCuratedText-Covid19-large.txt")
		ngramsFreqSorted := ngramsFreqsOfAll(corpusCuratedText, 5)
		for _, anNgramFreq := range ngramsFreqSorted {
			fmt.Println("N-GRAM: ", anNgramFreq.Key, anNgramFreq.Value)
		}
		fmt.Printf("\n")
		os.Exit(1)
	}

	// Allow go interfaces to be expanded into custom structs of our cache implementation
	gob.Register(CachedData{}) // For some reason, this declaration must be written on main function

	// fmt.Println("* Init cache ...")
	// cacheInit()

	fmt.Println("* Link bootstrapping ...")
	linkBootstraping()

	fmt.Println("* Init CSV ...")
	csvInit()

	fmt.Println("* Init curated corpus ...")
	iolib.String2file("\n", "./logs/corpusCuratedText.log")

	// Loop
	for numLinksProcessed := 0; ; numLinksProcessed++ {
		if !doNextLink(numLinksProcessed) {
			break
		}
	}

	fmt.Println("\n\n\n***** Done!!!")
}

// * Downloading url: http://(https://aasm.org/coronavirus-covid-19-faqs-cpap-sleep-apnea-patients/)panic: regexp: Compile(`(?i)\W((https)\W`): error parsing regexp: missing closing ): `(?i)\W((https)\W`

// goroutine 1 [running]:
// regexp.MustCompile(0xc000f022f0, 0x10, 0x7)
// 	/usr/lib/go-1.13/src/regexp/regexp.go:311 +0x152
// main.domainHadFailed(0xc000f022e0, 0x6, 0xc000f022e0)
// 	/home/informatica/projects/goDockerElasticsearch/goCrawler/crawler.go:461 +0x84
// main.download(0xc002a05e50, 0x4e, 0x0, 0xc002a05e50, 0x4e, 0xc009945ec0, 0x54, 0xc002cf3850, 0x4dd471)
// 	/home/informatica/projects/goDockerElasticsearch/goCrawler/crawler.go:187 +0xfe
// main.downloadCached(0xc002a05e50, 0x4e, 0x9694e9, 0x16, 0xc002cf3a90, 0x1, 0x1, 0x62, 0x0)
// 	/home/informatica/projects/goDockerElasticsearch/goCrawler/crawler.go:260 +0xc6
// main.doNextLink(0xcdf, 0x1)
// 	/home/informatica/projects/goDockerElasticsearch/goCrawler/crawler.go:1041 +0x143
// main.main()
// 	/home/informatica/projects/goDockerElasticsearch/goCrawler/crawler.go:1423 +0x2bd
// exit status 2
