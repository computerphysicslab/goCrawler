// From British National Corpus
// into a go map by https://github.com/computerphysicslab
// https://www.wordfrequency.info/100k_compare.asp
// unlemmatized frequencies
// all.num.gz file as of 2020-08-19
// nouns, verbs, adjectives, and adverbs

package goCorpusFreqLib

import (
	"bufio"
	"fmt"
	"log"
	"os"
)

type WordInfo struct {
	numTotal   int // repeated times appearing on the whole corpus, out of 100106029
	POStagging string
	numDocs    int // number of documents the word was found on, out of 4124
}

// var corpusFreqs = map[string]WordInfo{
// 	"!!WHOLE_CORPUS": {100106029, "!!ANY", 4124},
// 	"the":            {6187267, "at0", 4120},
// 	"of":             {2941444, "prf", 4108},
// 	"and":            {2682863, "cjc", 4120},
// 	"a":              {2126369, "at0", 4113},
// 	"in":             {1812609, "prp", 4109},
// 	"to":             {1620850, "to0", 4115},
// 	"it":             {1089186, "pnp", 4097},
// 	"is":             {998389, "vbz", 4097},
// }

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

func loadCorpus(filename string) map[string]WordInfo {
	if !fileExists(filename) {
		fmt.Printf("\nError: Corpus data not found on %s", filename)
		if filename == "./goCorpusFreqLib/all.num" {
			fmt.Printf("\nDownload all.num file like this:" + "\n\nmkdir goCorpusFreqLib\ncd goCorpusFreqLib\nwget \"http://www.kilgarriff.co.uk/BNClists/all.num.gz\"\ngunzip all.num.gz\ncd..\n\n")
		}
		os.Exit(1)
	}
	file, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
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
		// fmt.Println(l)
		numLine++
		// if numLine > 100000 {
		// 	panic(1)
		// }

		_, err := fmt.Sscanf(l, "%d %s %s %d", &numTotal, &word, &POStagging, &numDocs)
		if err != nil {
			log.Fatal(err)
		}

		if corpusFreqs[word].numTotal == 0 {
			// fmt.Printf("\n"+`"%s": {%d, "%s", %d},`, word, numTotal, POStagging, numDocs)
			corpusFreqs[word] = WordInfo{numTotal, POStagging, numDocs}
		}
	}
	fmt.Printf("\n\nlen(corpusFreqs) = %d\n", len(corpusFreqs))

	return corpusFreqs
}

func Init() {
	corpusFreqsEng = loadCorpus("./goCorpusFreqLib/all.num")
	corpusFreqsCont = loadCorpus("./goCorpusFreqLib/contemporaryEnglish.txt")

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
}

func Freq(token string) (numTotal int) {
	numTotal = corpusFreqs[token].numTotal

	return
}

func Test() {
	fmt.Printf("\n\n")
	fmt.Printf("THE: %d\n", Freq("the"))
	fmt.Printf("Shakespeare: %d\n", Freq("shakespeare"))
	fmt.Printf("Covid: %d\n", Freq("covid"))
	fmt.Printf("Blockchain: %d\n", Freq("blockchain"))
	fmt.Printf("Services: %d\n", Freq("services"))
}
