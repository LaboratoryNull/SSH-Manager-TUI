APP              := sshm
COMPAT_APP       := ssh-manager

ifeq ($(OS),Windows_NT)
SHELL            := cmd.exe
endif

PREFIX           ?= /usr/local
BINDIR           ?= $(PREFIX)/bin
DESTDIR          ?=
DISTDIR          ?= dist
INSTALL          ?= install
GITHUB_REPOSITORY ?= LaboratoryNull/SSH-Manager-TUI
UPDATE_URL       ?= https://github.com/$(GITHUB_REPOSITORY)/releases/latest/download/manifest.json

ifeq ($(OS),Windows_NT)
VERSION          ?= unknown
COMMIT           ?= unknown
BUILD_DATE       ?= unknown
else
NULLDEV          := /dev/null
GIT_DATE_FORMAT  := %cI

GIT_TAG          := $(shell git describe --tags --exact-match 2>$(NULLDEV))
GIT_COMMIT       := $(shell git rev-parse --short HEAD 2>$(NULLDEV))
GIT_BUILD_DATE   := $(shell git show -s --format=$(GIT_DATE_FORMAT) HEAD 2>$(NULLDEV))

VERSION          ?= $(if $(GIT_TAG),$(GIT_TAG),$(if $(GIT_COMMIT),dev-$(GIT_COMMIT),unknown))
COMMIT           ?= $(if $(GIT_COMMIT),$(GIT_COMMIT),unknown)
BUILD_DATE       ?= $(if $(GIT_BUILD_DATE),$(GIT_BUILD_DATE),unknown)
endif
RELEASE_BASE_URL ?= https://github.com/$(GITHUB_REPOSITORY)/releases/download/$(VERSION)
LDFLAGS          := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE) -X main.updateManifestURL=$(UPDATE_URL)

.PHONY: all build win windows test race vet fmt install uninstall update release clean

all: build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(APP) .

win windows:
	$(MAKE) build APP=$(APP).exe GOOS=windows GOARCH=amd64

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

# Installs into PREFIX/bin (/usr/local/bin by default).
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

# Builds the six supported release artifacts and their SHA-256 update
# manifest. VERSION must be a SemVer tag such as v1.2.0.
release: test
	@case "$(VERSION)" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "VERSION must be a SemVer tag, for example v1.2.0" >&2; exit 2;; esac
	$(INSTALL) -d "$(DISTDIR)"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-linux-amd64" .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-linux-arm64" .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-darwin-amd64" .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-darwin-arm64" .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-windows-amd64.exe" .
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DISTDIR)/$(APP)-windows-arm64.exe" .
	go run ./cmd/release-manifest -dir "$(DISTDIR)" -version "$(VERSION)" -base-url "$(RELEASE_BASE_URL)" -output "$(DISTDIR)/manifest.json"
	@echo "Release $(VERSION) is ready in $(DISTDIR)/"

clean:
	rm -f "$(APP)"
	rm -rf "$(DISTDIR)"
