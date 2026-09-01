APP              := sshm
COMPAT_APP       := ssh-manager
PREFIX           ?= /usr/local
BINDIR           ?= $(PREFIX)/bin
DESTDIR          ?=
DISTDIR          ?= dist
INSTALL          ?= install
VERSION          ?= $(shell git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD 2>/dev/null | sed 's/^/dev-/')
COMMIT           ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE       ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
UPDATE_URL       ?= https://git.dx.vlabs/infra/ssh-manager-tui/-/releases/permalink/latest/downloads/manifest.json
RELEASE_BASE_URL ?= https://git.dx.vlabs/infra/ssh-manager-tui/-/releases/$(VERSION)/downloads
LDFLAGS          := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE) -X main.updateManifestURL=$(UPDATE_URL)

.PHONY: all build test race vet fmt install uninstall update release clean

all: build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(APP) .

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

# Installs into ~/.local/bin by default. For a machine-wide installation use:
# sudo make install PREFIX=/usr/local
install: build
	$(INSTALL) -d "$(DESTDIR)$(BINDIR)"
	$(INSTALL) -m 0755 "$(APP)" "$(DESTDIR)$(BINDIR)/$(APP)"
	ln -sfn "$(APP)" "$(DESTDIR)$(BINDIR)/$(COMPAT_APP)"
	@echo "Installed $(APP) $(VERSION) into $(DESTDIR)$(BINDIR)"
	@echo "Run: $(APP)"

uninstall:
	rm -f "$(DESTDIR)$(BINDIR)/$(APP)" "$(DESTDIR)$(BINDIR)/$(COMPAT_APP)"

# Updates a source checkout without creating merge commits, verifies it and
# reinstalls into the same prefix. Installed release binaries use `sshm update`.
update:
	git pull --ff-only
	$(MAKE) test
	$(MAKE) install PREFIX="$(PREFIX)" BINDIR="$(BINDIR)" DESTDIR="$(DESTDIR)"

# Builds the four supported release artifacts and their SHA-256 update
# manifest. VERSION must be a SemVer tag such as v1.2.0.
release: test
	@case "$(VERSION)" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "VERSION must be a SemVer tag, for example v1.2.0" >&2; exit 2;; esac
	$(INSTALL) -d "$(DISTDIR)"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-linux-amd64" .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-linux-arm64" .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-darwin-amd64" .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-darwin-arm64" .
	go run ./cmd/release-manifest -dir "$(DISTDIR)" -version "$(VERSION)" -base-url "$(RELEASE_BASE_URL)" -output "$(DISTDIR)/manifest.json"
	@echo "Release $(VERSION) is ready in $(DISTDIR)/"

clean:
	rm -f "$(APP)"
	rm -rf "$(DISTDIR)"
