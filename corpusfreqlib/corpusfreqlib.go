// From British National Corpus
// into a go map by https://github.com/computerphysicslab
// https://www.wordfrequency.info/100k_compare.asp
// unlemmatized frequencies
// all.num.gz file as of 2020-08-19
// nouns, verbs, adjectives, and adverbs

// Package corpusfreqlib tells the frequency of a token/term on standard english & contemporary corpora
package corpusfreqlib

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

type WordInfo struct {
	numTotal   int // repeated times appearing on the whole corpus, out of 100106029
	POStagging string
	numDocs    int // number of documents the word was found on, out of 4124
}

// 	"!!WHOLE_CORPUS": {100106029, "!!ANY", 4124},
// 	"the":            {6187267, "at0", 4120},
// 	"of":             {2941444, "prf", 4108},
// 	"and":            {2682863, "cjc", 4120},
// 	"a":              {2126369, "at0", 4113},
// 	"in":             {1812609, "prp", 4109},
// 	"to":             {1620850, "to0", 4115},
// 	"it":             {1089186, "pnp", 4097},
// 	"is":             {998389, "vbz", 4097},

var corpusFreqs = map[string]WordInfo{}     // English corpus (classical + contenmporary)
var corpusFreqsEng = map[string]WordInfo{}  // Classical English
var corpusFreqsCont = map[string]WordInfo{} // Contenmporary English

// fileExists checks if a file exists and is not a directory before we try using it to prevent further errors.
// https://golangcode.com/check-if-a-file-exists/
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// loadCorpus reads a BNC-style frequency file into a map.
func loadCorpus(filename string) (map[string]WordInfo, error) {
	if !fileExists(filename) {
		return nil, fmt.Errorf("corpus data not found: %s", filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var corpusFreqs = make(map[string]WordInfo)

	numLine := 0
	var l string
	var word, POStagging string
	var numTotal, numDocs int
	scanner := bufio.NewScanner(file)
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	for scanner.Scan() {
		l = scanner.Text()
		numLine++

		_, err := fmt.Sscanf(l, "%d %s %s %d", &numTotal, &word, &POStagging, &numDocs)
		if err != nil {
			return nil, err
		}

		if corpusFreqs[word].numTotal == 0 {
			corpusFreqs[word] = WordInfo{numTotal, POStagging, numDocs}
		}
	}
	fmt.Printf("\n\nlen(corpusFreqs) = %d\n", len(corpusFreqs))

	return corpusFreqs, nil
}

const freqIndexFile = "./corpusfreqlib/freq-index.json"
const freqIndexGzFile = "./corpusfreqlib/freq-index.json.gz"

// loadFreqIndex loads the compact JSON index when it is available.
func loadFreqIndex() bool {
	var b []byte
	if file, err := os.Open(freqIndexGzFile); err == nil {
		reader, err := gzip.NewReader(file)
		if err == nil {
			b, err = io.ReadAll(reader)
			reader.Close()
		}
		file.Close()
	}
	if len(b) == 0 {
		b, _ = os.ReadFile(freqIndexFile)
	}
	if len(b) == 0 {
		return false
	}
	index := make(map[string]int)
	if err := json.Unmarshal(b, &index); err != nil || len(index) == 0 || index["the"] == 0 {
		return false
	}
	corpusFreqs = make(map[string]WordInfo, len(index))
	for token, total := range index {
		corpusFreqs[token] = WordInfo{numTotal: total}
	}
	return true
}

// saveFreqIndex persists only the totals used by Freq.
func saveFreqIndex() {
	index := make(map[string]int, len(corpusFreqs))
	for token, item := range corpusFreqs {
		index[token] = item.numTotal
	}
	b, err := json.Marshal(index)
	if err != nil {
		log.Println("cannot encode frequency index:", err)
		return
	}
	if err := os.WriteFile(freqIndexFile, b, 0644); err != nil {
		log.Println("cannot write frequency index:", err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err == nil {
		if err := zw.Close(); err == nil {
			os.WriteFile(freqIndexGzFile, buf.Bytes(), 0644)
		}
	}
}

// GenerateIndex rebuilds the compressed frequency index from BNC-style sources.
func GenerateIndex(bncPath, contemporaryPath, outputPath string) error {
	eng, err := loadCorpus(bncPath)
	if err != nil {
		return err
	}
	cont, err := loadCorpus(contemporaryPath)
	if err != nil {
		return err
	}
	theEng, theCont := eng["the"].numTotal, cont["the"].numTotal
	if theEng == 0 || theCont == 0 {
		return fmt.Errorf("both source corpora must contain the token 'the'")
	}
	factor := float64(theEng) / float64(theCont)

	index := make(map[string]int, len(eng)+len(cont))
	for token, item := range eng {
		index[token] = item.numTotal
	}
	for token, item := range cont {
		index[token] = eng[token].numTotal + int(factor*float64(item.numTotal))
	}
	b, err := json.Marshal(index)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(outputPath, buf.Bytes(), 0644)
}

// Init loads and normalizes the English reference corpora.
func Init() error {
	if loadFreqIndex() {
		return nil
	}
	var err error
	corpusFreqsEng, err = loadCorpus("./corpusfreqlib/all.num")
	if err != nil {
		return fmt.Errorf("corpus frequency index unavailable: %w (run 'make freq-index' with the full all.num source or restore corpusfreqlib/freq-index.json.gz)", err)
	}
	corpusFreqsCont, err = loadCorpus("./corpusfreqlib/contemporaryEnglish.txt")
	if err != nil {
		return err
	}

	// both copora have in common the word "the" as a metric to perform a normalization
	ContFactor := float64(corpusFreqsEng["the"].numTotal) / float64(corpusFreqsCont["the"].numTotal) // normalization factor for contemporary english corpus
	fmt.Printf("\n\nContFactor: %f", ContFactor)

	// merging both corpora into an integrated one
	corpusFreqs = corpusFreqsEng
	Test()

	for token, item := range corpusFreqsCont {
		corpusFreqs[token] = WordInfo{corpusFreqsEng[token].numTotal + int(ContFactor*float64(item.numTotal)), item.POStagging, 0}
	}

	Test()
	saveFreqIndex()
	return nil
}

// Freq returns the standard English frequency of a token.
func Freq(token string) (numTotal int) {
	numTotal = corpusFreqs[token].numTotal

	return
}

// Test prints a few sample frequencies for manual verification.
func Test() {
	fmt.Printf("\n\n")
	fmt.Printf("THE: %d\n", Freq("the"))
	fmt.Printf("Shakespeare: %d\n", Freq("shakespeare"))
	fmt.Printf("Covid: %d\n", Freq("covid"))
	fmt.Printf("Blockchain: %d\n", Freq("blockchain"))
	fmt.Printf("Services: %d\n", Freq("services"))
}
