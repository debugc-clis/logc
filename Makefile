BINARY := logc
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test install clean dist

build:
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

test:
	go test ./...

install:
	go install -trimpath -ldflags "$(LDFLAGS)" .

clean:
	rm -rf bin dist

dist: clean test
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/logc-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/logc-linux-arm64 .
	GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/logc-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/logc-darwin-arm64 .
