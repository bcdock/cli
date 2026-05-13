VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BCDOCK   := bin/bcdock
BCDOCKADM := bin/bcdockadm
DOCS_OUT ?= ../../docs-site/public/docs/cli/reference

# Version + commit are injected into the public package (where the version
# command + skew probe live). Same flag values apply to both binaries.
LDFLAGS := -X github.com/bcdock/cli/internal/cli.version=$(VERSION) \
           -X github.com/bcdock/cli/internal/cli.commit=$(COMMIT)

# Admin source is only present in the upstream monorepo. The public mirror
# (github.com/bcdock/cli) drops cmd/bcdockadm/ via the filter, so any admin
# target must be conditional on the directory existing. Detect at make time.
ADMIN_PRESENT := $(wildcard cmd/bcdockadm/main.go)
ADMIN_TARGETS := $(if $(ADMIN_PRESENT),bcdockadm,)

.PHONY: build bcdock bcdockadm install install-admin test clean docs

build: bcdock $(ADMIN_TARGETS)

bcdock:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BCDOCK) ./cmd/bcdock/

bcdockadm:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BCDOCKADM) ./cmd/bcdockadm/

install: build
	cp $(BCDOCK) $(shell go env GOPATH)/bin/bcdock
	@$(MAKE) -s install-admin

install-admin:
ifneq ($(ADMIN_PRESENT),)
	cp $(BCDOCKADM) $(shell go env GOPATH)/bin/bcdockadm
endif

test:
	go test ./...

# Regenerate docs-site CLI reference pages from cobra (public surface only).
# Cleans the output dir first so removed commands don't leave orphan markdown
# (cobra docgen creates files for current commands but doesn't delete stale
# ones). The cli-reference-stale-check.yml CI guard relies on the post-regen
# tree exactly matching the committed tree; orphans would mask removals.
docs: bcdock
	rm -rf $(DOCS_OUT)
	mkdir -p $(DOCS_OUT)
	$(BCDOCK) docgen --out $(DOCS_OUT)

clean:
	rm -f $(BCDOCK) $(BCDOCKADM)
