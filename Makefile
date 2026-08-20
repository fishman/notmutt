# notmutt - build and test targets. Default build carries the Lua runtime
# (R8); TAGS overrides for other combinations, e.g. `make build TAGS="lua cli"`
# or a Lua-free `make build TAGS=""`.

GO      ?= go
TAGS    ?= lua
BIN     ?= notmutt
FUZZ    ?= FuzzRenderHTML
FUZZTIME?= 30s

GO_CMD   = cd src && $(GO)
GO_TAGS  = -tags "$(TAGS)"

.PHONY: all build test fuzz vet fmt clean

all: build

build:
	$(GO_CMD) build $(GO_TAGS) -o ../$(BIN) .

test:
	$(GO_CMD) test $(GO_TAGS) ./...

fuzz:
	$(GO_CMD) test $(GO_TAGS) -run '^$$' -fuzz "$(FUZZ)" -fuzztime "$(FUZZTIME)"

vet:
	$(GO_CMD) vet $(GO_TAGS) ./...

fmt:
	cd src && gofmt -l -w .

clean:
	rm -f src/$(BIN)
