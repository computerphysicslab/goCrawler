BINARY := crawler
PROFILE ?=

.PHONY: all build run test selftest fmt vet clean

all: build

build:
	go build ./...
	go build -o $(BINARY) crawler.go

run:
	@test -n "$(PROFILE)" || (echo "Usage: make run PROFILE=<name>" && exit 1)
	go run crawler.go $(PROFILE)

test:
	go test ./...

selftest:
	go run crawler.go --selftest

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -f logs/*.log
	rm -rf cache
