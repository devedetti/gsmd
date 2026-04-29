BINARY := gsmd
GOOS   ?= linux
GOARCH ?= amd64

.PHONY: build clean test vet fmt all

all: vet test build

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="-s -w" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

clean:
	rm -f $(BINARY)
