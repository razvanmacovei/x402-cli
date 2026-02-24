VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BINARY  = x402-cli
LDFLAGS = -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build run test clean lint

build:
	go build $(LDFLAGS) -o bin/$(BINARY) .

run: build
	./bin/$(BINARY) $(ARGS)

test:
	go build $(LDFLAGS) -o /dev/null .

clean:
	rm -rf bin/ dist/

lint:
	go vet ./...
