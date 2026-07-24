# Build metadata — overridable, defaults from git
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo vDEV)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo NONE)
DATE   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo UNKNOWN)

# Cross-compilation — supply GOOS/GOARCH/CGO_ENABLED from env or make args
GOOS        ?= $(shell go env GOOS)
GOARCH      ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0

# ldflags stripped down, with version metadata baked in
LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

# Output binary path, e.g. dist/sqlite-vault-verify_linux_amd64/sqlite-vault-verify
OUTPUT = dist/sqlite-vault-verify_$(GOOS)_$(GOARCH)/sqlite-vault-verify

.PHONY: build
build:
	@mkdir -p $(dir $(OUTPUT))
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) \
		go build -ldflags="$(LDFLAGS)" -o $(OUTPUT) ./cmd/sqlite-vault-verify

.PHONY: output-path
output-path:
	@echo $(OUTPUT)

.PHONY: clean
clean:
	rm -rf dist

.PHONY: release
release: build
