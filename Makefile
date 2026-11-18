BINARY := crawler
GO ?= go1.26.11
PROFILE ?=
FREQ_BNC ?= corpusfreqlib/all.num
FREQ_CONT ?= corpusfreqlib/contemporaryEnglish.txt

.PHONY: all build run test selftest fmt vet freq-index clean

all: build

build:
	$(GO) build ./...
	$(GO) build -o $(BINARY) crawler.go

run:
	@test -n "$(PROFILE)" || (echo "Usage: make run PROFILE=<name>" && exit 1)
	$(GO) run crawler.go $(PROFILE)

test:
	$(GO) test ./...

selftest:
	$(GO) run crawler.go --selftest

fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

# Rebuild the compact English frequency index from local source files.
# The 18 MB all.num source is not tracked; supply FREQ_BNC=/path/to/all.num.
freq-index:
	$(GO) run ./tools/generate_freq_index.go -bnc $(FREQ_BNC) -contemporary $(FREQ_CONT) -output corpusfreqlib/freq-index.json.gz

clean:
	rm -f $(BINARY)
	rm -f logs/*.log
	rm -rf cache
