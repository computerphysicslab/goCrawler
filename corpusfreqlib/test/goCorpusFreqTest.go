// From British National Corpus
// into a go map by https://github.com/computerphysicslab
// https://www.wordfrequency.info/100k_compare.asp
// unlemmatized frequencies
// all.num.gz file as of 2020-08-19
// nouns, verbs, adjectives, and adverbs

package main

import (
	"fmt"
	goCorpusFreqLib "goCrawler/corpusfreqlib"
)

// main prints sample frequencies from the English reference corpus.
func main() {
	if err := goCorpusFreqLib.Init(); err != nil {
		fmt.Println("corpus init error:", err)
		return
	}
	goCorpusFreqLib.Test()
}
