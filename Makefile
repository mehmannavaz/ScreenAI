# ScreenAI — build and test

BINARY  := bin/screenai
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/mehmannavaz/ScreenAI/internal/cli.Version=$(VERSION)

.PHONY: all build test vet fmt lint clean windows linux demo install

all: build

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/screenai

windows:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY).exe ./cmd/screenai

linux:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 ./cmd/screenai

test:
	CGO_ENABLED=0 go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint: vet test

demo: build
	$(BINARY) demo

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/screenai

clean:
	rm -rf bin results component* screenai*.zip
