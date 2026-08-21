# notmutt - build and test targets. Default build carries the Lua runtime
# (R8); TAGS overrides for other combinations, e.g. `make build TAGS="lua cli"`
# or a Lua-free `make build TAGS=""`.

GO      ?= go
TAGS    ?= lua
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

.PHONY: all build test test-race fuzz vet format clean

all: build

build:
	$(GO_CMD) build $(GO_TAGS) -o ../$(BIN) .

test:
	$(GO_CMD) test $(GO_TAGS) ./...

test-race:
	$(GO_CMD) test $(GO_TAGS) -race ./...

fuzz:
	$(GO_CMD) test $(GO_TAGS) -run '^$$' -fuzz "$(FUZZ)" -fuzztime "$(FUZZTIME)"

vet:
	$(GO_CMD) vet $(GO_TAGS) ./...

# format: the CI gofmt gate (default); `make format FMT=write` applies
# the fixes instead. One file list serves both modes.
FMT ?= check
FMT_LIST := gofmt -l . | grep -v '^vendor/'

format:
	cd src && if [ "$(FMT)" = write ]; then $(FMT_LIST) | xargs -r gofmt -w; else test -z "$$($(FMT_LIST))"; fi

clean:
	rm -f src/$(BIN)
