BINARY  := tokencounter
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFILES := $(shell find . -name '*.go' -not -path './vendor/*')

PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: all build install test race vet fmt fmt-check tidy clean run dist docker help

all: fmt-check vet test build ## Format-check, vet, test, and build

build: ## Build the binary into ./$(BINARY)
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) .

install: ## Install the binary into GOPATH/bin
	go install -ldflags '$(LDFLAGS)' .

test: ## Run the test suite
	go test ./...

race: ## Run the test suite with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go files
	gofmt -w $(GOFILES)

fmt-check: ## Fail if any Go file is not gofmt-clean
	@out="$$(gofmt -l $(GOFILES))"; \
	if [ -n "$$out" ]; then echo "Not gofmt-clean:"; echo "$$out"; exit 1; fi

tidy: ## Tidy go.mod
	go mod tidy

clean: ## Remove build artifacts
	rm -rf $(BINARY) dist

run: build ## Build then run; pass args via ARGS="--url ... --model ..."
	./$(BINARY) $(ARGS)

dist: ## Cross-compile release binaries into ./dist
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-$$os-$$arch$$ext .; \
	done

docker: ## Build the Docker image tagged $(BINARY):$(VERSION)
	docker build -t $(BINARY):$(VERSION) .

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
