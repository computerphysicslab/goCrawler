BINARY := crawler
PROFILE ?=

.PHONY: all build run test fmt vet clean

all: build

build:
	go build ./...
	go build -o $(BINARY) crawler.go

run:
	@test -n "$(PROFILE)" || (echo "Usage: make run PROFILE=<name>" && exit 1)
	@if [ ! -f "$(PROFILE).yaml" ] && [ -f "profiles/$(PROFILE).yaml" ]; then \
		cp "profiles/$(PROFILE).yaml" .; \
	fi
	go run crawler.go $(PROFILE)

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -f logs/*.log
	rm -rf cache
