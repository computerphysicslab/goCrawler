// From British National Corpus
// into a go map by https://github.com/computerphysicslab
// https://www.wordfrequency.info/100k_compare.asp
// unlemmatized frequencies
// all.num.gz file as of 2020-08-19
// nouns, verbs, adjectives, and adverbs

package main

import (
	goCorpusFreqLib "goCrawler/goCorpusFreqLib"
)

func main() {
	goCorpusFreqLib.Init()
	goCorpusFreqLib.Test()
}
