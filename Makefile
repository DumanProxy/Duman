VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS  = -ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)"

.PHONY: build test clean cross lint

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-client ./cmd/duman-client
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-relay ./cmd/duman-relay

test:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

test-verbose:
	go test -race -v -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean:
	rm -rf bin/ coverage.out coverage.html

cross:
	# Client binaries
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-client-linux-amd64 ./cmd/duman-client
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-client-linux-arm64 ./cmd/duman-client
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-client-darwin-amd64 ./cmd/duman-client
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-client-darwin-arm64 ./cmd/duman-client
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-client-windows-amd64.exe ./cmd/duman-client
	# Relay binaries (Linux only)
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-relay-linux-amd64 ./cmd/duman-relay
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-relay-linux-arm64 ./cmd/duman-relay

lint:
	go vet ./...
