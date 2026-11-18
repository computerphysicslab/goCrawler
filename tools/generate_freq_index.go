// Command generate_freq_index rebuilds corpusfreqlib/freq-index.json.gz from
// local BNC-style frequency files. The large all.num source is not shipped in
// the repository; supply it with -bnc.
package main

import (
	"flag"
	"fmt"

	"goCrawler/corpusfreqlib"
)

func main() {
	bnc := flag.String("bnc", "corpusfreqlib/all.num", "BNC-style all.num source file")
	contemporary := flag.String("contemporary", "corpusfreqlib/contemporaryEnglish.txt", "contemporary English frequency file")
	output := flag.String("output", "corpusfreqlib/freq-index.json.gz", "destination .gz index")
	flag.Parse()

	if err := corpusfreqlib.GenerateIndex(*bnc, *contemporary, *output); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("wrote", *output)
}
