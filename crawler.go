// aggregates coronavirus related research documents
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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackdanger/collectlinks"
	snowballeng "github.com/kljensen/snowball/english"
	"github.com/patrickmn/go-cache"
	"github.com/spf13/viper"
	"jaytaylor.com/html2text"

	"github.com/computerphysicslab/goPackages/goDebug"

	goCorpusFreqLib "goCrawler/goCorpusFreqLib"
)

/******************************************************************************/
/******************************************************************************/
/*********************** CONFIG ***********************************************/
/******************************************************************************/
/******************************************************************************/

var regexBannedDomains string
var regexLinkBannedTokens string
var curatedDomains string
var regexLinkOk string
var engStopWordsWOthe string
var engStopWords string
var engLowRelevancyWords string
var regexStopwords string
var regexRankingKeywords string
var proxyHost string
var proxyUser string
var proxyPass string
var downloadTimeout time.Duration
var bootstrapingLinks []string

/******************************************************************************/
/******************************************************************************/
/*********************** FUNCTIONS ********************************************/
/******************************************************************************/
/******************************************************************************/

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
		err := copyFileContents("./cache/cachePersistent.dat", "./cache/cachePersistent.backup")
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

	goDebug.Print("linkBootstraping", LPool)
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
	f, err := os.OpenFile("./logs/domainFailed.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer f.Close()
	if _, err := f.WriteString(domain + "\n"); err != nil {
		log.Println(err)
	}
}

func domainHadFailed(domain string) bool {
	file, err := os.Open("./logs/domainFailed.log")
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
	// goDebug.Print(ar)

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
	// goDebug.Print(regex)
	rs := regexp.MustCompile(regex)
	// t2 := rs.ReplaceAllString(text, " _ ")
	// goDebug.Print(t2)
	matches := rs.FindAllStringIndex(text, -1)
	// goDebug.Print(matches)

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
	outfile, _  = os.Create("./logs/download.log")
	logDownload = log.New(outfile, "", 0)
)

var (
	outfile2, _ = os.Create("./logs/ranking.log")
	logRanking  = log.New(outfile2, "", 0)
)

var (
	outfile3, _ = os.Create("./logs/cgi.log")
	logCGI      = log.New(outfile3, "", 0)
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
	// goDebug.Print(paragraphs)

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

		// substracting english words frequencies
		corpusFreqsWithoutEnglish := make(freq) // specific corpus token frequencies w/o english baseline

		var intercorpusScaleFactor float64
		intercorpusContrast := 20.0
		if g[0].Key == "the" {
			intercorpusScaleFactor = float64(1+goCorpusFreqLib.Freq("the")) / float64(g[0].Value)
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
		for _, keyValue := range g {
			// By division:
			// corpusFreqsWithoutEnglish[keyValue.Key] = int(intercorpusScaleFactor * float64(keyValue.Value) / float64(1+goCorpusFreqLib.Freq(keyValue.Key)))
			// By substraction:
			corpusFreqsWithoutEnglish[keyValue.Key] = keyValue.Value - int(intercorpusContrast*float64(1+goCorpusFreqLib.Freq(keyValue.Key))/intercorpusScaleFactor)
			// fmt.Printf("\nkeyValue=%+v [eng: %d] [corpusFreqsWithoutEnglish: %d]", keyValue, goCorpusFreqLib.Freq(keyValue.Key), corpusFreqsWithoutEnglish[keyValue.Key])
		}
		corpusFreqsWithoutEnglishSorted := rSortFreq(corpusFreqsWithoutEnglish)
		fmt.Println("\n\nCorpus frequencies w/o Eng.: ", corpusFreqsWithoutEnglishSorted[:100])

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

func yamlInit() {
	argsWithoutProg := os.Args[1:]
	viper.SetConfigName(argsWithoutProg[0]) // name of config file (without extension)
	viper.AddConfigPath(".")                // look for config in the working directory
	err := viper.ReadInConfig()             // Find and read the config file
	if err != nil {                         // Handle errors reading the config file
		panic(fmt.Errorf("Fatal error config file: %s", err))
	}
	regexBannedDomains = viper.GetString("regexBannedDomains")
	regexLinkBannedTokens = viper.GetString("regexLinkBannedTokens")
	curatedDomains = viper.GetString("curatedDomains")
	regexLinkOk = `(?i)^https*://.*(` + viper.GetString("linkOk") + `|` + curatedDomains + `)`
	engStopWordsWOthe = viper.GetString("engStopWordsWOthe")
	engStopWords = `the|` + engStopWordsWOthe
	engLowRelevancyWords = `|` + viper.GetString("engLowRelevancyWords")
	regexStopwords = `(?i)\W([0-9]+|.|..|` + engStopWordsWOthe + engLowRelevancyWords + `|` + viper.GetString("specialStopwords") + `)\W`
	regexRankingKeywords = viper.GetString("regexRankingKeywords")
	proxyHost = viper.GetString("proxyHost")
	proxyUser = viper.GetString("proxyUser")
	proxyPass = viper.GetString("proxyPass")
	downloadTimeout = time.Duration(viper.GetInt("downloadTimeout")) * time.Second
	bootstrapingLinks = viper.GetStringSlice("bootstrapingLinks")

	fmt.Printf("\n\nargsWithoutProg: %+v", argsWithoutProg)
	fmt.Printf("\n\nregexBannedDomains: %s", regexBannedDomains)
	fmt.Printf("\n\nregexLinkBannedTokens: %s", regexLinkBannedTokens)
	fmt.Printf("\n\ncuratedDomains: %s", curatedDomains)
	fmt.Printf("\n\nregexLinkOk: %s", regexLinkOk)
	fmt.Printf("\n\nengStopWordsWOthe: %s", engStopWordsWOthe)
	fmt.Printf("\n\nengStopWords: %s", engStopWords)
	fmt.Printf("\n\nengLowRelevancyWords: %s", engLowRelevancyWords)
	fmt.Printf("\n\nregexStopwords: %s", regexStopwords)
	fmt.Printf("\n\nregexRankingKeywords: %s", regexRankingKeywords)
	fmt.Printf("\n\nproxyHost: %s", proxyHost)
	fmt.Printf("\n\nproxyUser: %s", proxyUser)
	fmt.Printf("\n\nproxyPass: %s", proxyPass)
	fmt.Printf("\n\ndownloadTimeout: %+v", downloadTimeout)
	fmt.Printf("\n\nbootstrapingLinks: %+v", bootstrapingLinks)
}

func main() {
	fmt.Println("* Loading YAML config ...")
	yamlInit()

	fmt.Println("* Init English corpus ...")
	goCorpusFreqLib.Init()

	// Allow go interfaces be expanded into custom structs of our cache implementation
	gob.Register(CachedData{}) // For some reason, this declaration must be written on main function

	fmt.Println("* Init cache ...")
	cacheInit()

	fmt.Println("* Link bootstrapping ...")
	linkBootstraping()

	// Tokenizer frequencies
	f = make(freq) // specific corpus token frequencies
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

	for {
		if !doNextLink() {
			break
		}
	}
	fmt.Println("***** Done!!!")
}
