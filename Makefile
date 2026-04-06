VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS  = -ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)"

.PHONY: build test clean cross lint keygen release

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-client ./cmd/duman-client
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/duman-relay ./cmd/duman-relay

test:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

test-short:
	go test -short ./...

test-verbose:
	go test -race -v -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean:
	rm -rf bin/ dist/ coverage.out coverage.html

cross:
	# Client binaries (all platforms)
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-client-linux-amd64 ./cmd/duman-client
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-client-linux-arm64 ./cmd/duman-client
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-client-darwin-amd64 ./cmd/duman-client
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-client-darwin-arm64 ./cmd/duman-client
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-client-windows-amd64.exe ./cmd/duman-client
	# Relay binaries (all platforms)
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-relay-linux-amd64 ./cmd/duman-relay
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-relay-linux-arm64 ./cmd/duman-relay
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-relay-darwin-amd64 ./cmd/duman-relay
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-relay-darwin-arm64 ./cmd/duman-relay
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/duman-relay-windows-amd64.exe ./cmd/duman-relay

release: cross
	cd dist && tar czf duman-relay-linux-amd64.tar.gz duman-relay-linux-amd64
	cd dist && tar czf duman-relay-linux-arm64.tar.gz duman-relay-linux-arm64
	cd dist && tar czf duman-client-linux-amd64.tar.gz duman-client-linux-amd64
	cd dist && tar czf duman-client-linux-arm64.tar.gz duman-client-linux-arm64
	cd dist && tar czf duman-relay-darwin-amd64.tar.gz duman-relay-darwin-amd64
	cd dist && tar czf duman-relay-darwin-arm64.tar.gz duman-relay-darwin-arm64
	cd dist && tar czf duman-client-darwin-amd64.tar.gz duman-client-darwin-amd64
	cd dist && tar czf duman-client-darwin-arm64.tar.gz duman-client-darwin-arm64

keygen:
	@go run ./cmd/duman-client keygen

lint:
	go vet ./...
