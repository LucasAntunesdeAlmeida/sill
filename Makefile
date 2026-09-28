VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)

FUZZTIME ?= 30s

.PHONY: build install test lint bench fuzz dist clean

build:
	go build -ldflags "$(LDFLAGS)" ./cmd/sill

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/sill

test:
	go test ./...

lint:
	gofmt -l . && go vet ./...

bench:
	go test -run '^$$' -bench . -benchmem ./...

# Run each fuzz target for FUZZTIME. The seeds alone already run in `make test`.
fuzz:
	go test -run '^$$' -fuzz '^FuzzSetStatusLine$$' -fuzztime $(FUZZTIME) ./internal/install
	go test -run '^$$' -fuzz '^FuzzParse$$' -fuzztime $(FUZZTIME) ./internal/config
	go test -run '^$$' -fuzz '^FuzzRender$$' -fuzztime $(FUZZTIME) ./internal/render
	go test -run '^$$' -fuzz '^FuzzScanSplit$$' -fuzztime $(FUZZTIME) ./internal/transcript

# Cross-compile every release target into dist/.
dist:
	@mkdir -p dist
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; [ "$$os" = windows ] && ext=.exe; \
		echo "building $$target"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/sill-$$os-$$arch$$ext ./cmd/sill; \
	done

clean:
	rm -rf dist sill sill.exe
