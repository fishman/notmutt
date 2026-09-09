# notmutt - build and test targets. Default build carries the Lua runtime
# (R8); TAGS overrides for other combinations, e.g. `make build TAGS="lua cli"`
# or a Lua-free `make build TAGS=""`.

GO      ?= go
TAGS    ?= lua mcp crm
BIN     ?= notmutt
FUZZ    ?= FuzzRenderHTML
FUZZTIME?= 30s

# REFSFROMTERMS=1 links the notmuch fork with the term-list ref getters
# (docs/refs-from-terms.md); the default build targets stock notmuch,
# where the walk packs empty reference chains.
ifeq ($(REFSFROMTERMS),1)
TAGS += refsfromterms
endif

GO_CMD   = cd src && $(GO)
GO_TAGS  = -tags "$(TAGS)"

.PHONY: all build test test-race fuzz vet format check clean

all: build

build:
	$(GO_CMD) build $(GO_TAGS) -o ../$(BIN) .

# build-cli: the Apache-clean variant (no libnotmuch link). cgo (default)
# links GPL libnotmuch; the cli tag drops go.notmuch entirely, so the
# binary calls the notmuch CLI as a subprocess. Both ship at release.
build-cli:
	$(GO_CMD) build -tags "$(TAGS) cli" -o ../notmutt-cli .

test:
	$(GO_CMD) test $(GO_TAGS) ./...

test-race:
	$(GO_CMD) test $(GO_TAGS) -race ./...

fuzz:
	$(GO_CMD) test $(GO_TAGS) -run '^$$' -fuzz "$(FUZZ)" -fuzztime "$(FUZZTIME)"

vet:
	$(GO_CMD) vet $(GO_TAGS) ./...

# format applies gofmt to the source tree; check is the CI gate.
# GOFMT_LIST lists the unformatted files outside vendor (one list, both
# modes): format feeds it to gofmt -w, check fails if it is non-empty.
GOFMT_LIST := gofmt -l . | grep -v '^vendor/'

format:
	cd src && $(GOFMT_LIST) | xargs -r gofmt -w

# check: the CI gate - gofmt-clean, vet, and the tagged test run. Run
# `make format` first if the gofmt step fails.
check:
	cd src && test -z "$$($(GOFMT_LIST))"
	$(GO_CMD) vet $(GO_TAGS) ./...
	$(GO_CMD) test $(GO_TAGS) ./...

clean:
	rm -f src/$(BIN)
