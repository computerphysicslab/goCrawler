BINARY := crawler
GO ?= go1.26.11
PROFILE ?=

.PHONY: all build run test selftest fmt vet clean

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

clean:
	rm -f $(BINARY)
	rm -f logs/*.log
	rm -rf cache
