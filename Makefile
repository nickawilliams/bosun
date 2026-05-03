SHELL := /usr/bin/env bash

# ============================================================================
# Project Metadata
# ============================================================================

PROJECT_YAML := project.yaml
BINARY := $(shell yq -r '.binary' $(PROJECT_YAML))
export PKG_BINARY := $(BINARY)
export PKG_DESCRIPTION := $(shell yq -r '.description' $(PROJECT_YAML))
export PKG_HOMEPAGE := $(shell yq -r '.homepage' $(PROJECT_YAML))
export PKG_LICENSE := $(shell yq -r '.license' $(PROJECT_YAML))
export PKG_MAINTAINER_NAME := $(shell yq -r '.maintainer.name' $(PROJECT_YAML))
export PKG_MAINTAINER_EMAIL := $(shell yq -r '.maintainer.email' $(PROJECT_YAML))

# ============================================================================
# Source & Output
# ============================================================================

SRC := $(shell find . -name '*.go')
OUT_DIR := .out
BUILD_BIN := $(OUT_DIR)/build/$(BINARY)

# ============================================================================
# Version
# ============================================================================

GIT_TAG := $(shell git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)
GIT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
EXACT_TAG := $(shell git describe --tags --exact-match 2>/dev/null)
VERSION ?= $(if $(EXACT_TAG),$(EXACT_TAG),$(GIT_TAG)-dev.$(GIT_SHA))
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.date=$(BUILD_DATE)

# ============================================================================
# Tools
# ============================================================================

GO ?= go
GIT_CLIFF ?= git-cliff
MAIN_PKG := ./cmd/bosun

export CARGO_HOME ?= $(CURDIR)/.cache/cargo

# ============================================================================
# Publishing
# ============================================================================

TAP_REPO ?= nickawilliams/homebrew-tap
TAP_FORMULA_PATH := Formula/bosun.rb
TAP_FORMULA := packaging/homebrew/bosun.rb

PORT_REPO ?= nickawilliams/fork-macports-ports
PORT_PULLREQUEST ?= false
PORTFILE_PATH := devel/bosun/Portfile
PORTFILE := packaging/macports/Portfile

# ============================================================================
# Install Paths
# ============================================================================

PREFIX ?= /usr/local/bin
PREFIX_ROOT := $(patsubst %/,%,$(dir $(PREFIX)))
INSTALL_BIN := $(PREFIX)/$(BINARY)
MANPREFIX ?= $(PREFIX_ROOT)/share/man
MANDIR := $(MANPREFIX)/man1
MANPAGE := bosun.1
MANPAGE_SRC := contrib/man/$(MANPAGE)
INSTALL_MAN := $(MANDIR)/$(MANPAGE)

ZSH_DIR := $(HOME)/.zsh
BASH_DIR := $(HOME)/.bash_completion.d
FISH_DIR := $(HOME)/.config/fish/completions

ZSH_SCRIPT_NAME := bosun.zsh
BASH_SCRIPT_NAME := bosun.bash
FISH_SCRIPT_NAME := bosun.fish

ZSH_SCRIPT_SRC := contrib/completions/zsh/$(ZSH_SCRIPT_NAME)
BASH_SCRIPT_SRC := contrib/completions/bash/$(BASH_SCRIPT_NAME)
FISH_SCRIPT_SRC := contrib/completions/fish/$(FISH_SCRIPT_NAME)

INSTALL_ZSH := $(ZSH_DIR)/$(ZSH_SCRIPT_NAME)
INSTALL_BASH := $(BASH_DIR)/$(BASH_SCRIPT_NAME)
INSTALL_FISH := $(FISH_DIR)/$(FISH_SCRIPT_NAME)

INSTALL_BIN_DIR := $(dir $(INSTALL_BIN))
INSTALL_ZSH_DIR := $(dir $(INSTALL_ZSH))
INSTALL_BASH_DIR := $(dir $(INSTALL_BASH))
INSTALL_FISH_DIR := $(dir $(INSTALL_FISH))
OMZ_CUSTOM ?= $(HOME)/.oh-my-zsh/custom
OMZ_PLUGIN_DIR := $(OMZ_CUSTOM)/plugins/bosun
OMZ_PLUGIN_SRC := $(ZSH_SCRIPT_SRC)
OMZ_PLUGIN_DEST := $(OMZ_PLUGIN_DIR)/bosun.plugin.zsh

# ============================================================================
# Release
# ============================================================================

RELEASE_BUMP_TYPE ?= patch
RELEASE_COMMIT_FLAG := $(OUT_DIR)/release_committed

# ============================================================================
# Macros
# ============================================================================

# $(call sudo-install,<src>,<dest>,<destdir>,<mode>)
define sudo-install
@if [ -w $(3) ]; then \
	install -d $(3); \
	install -m$(4) $(1) $(2); \
else \
	echo "Elevated permissions required — using sudo"; \
	sudo install -d $(3); \
	sudo install -m$(4) $(1) $(2); \
fi
endef

# $(call sudo-remove,<file>,<dir>)
define sudo-remove
@if [ -w $(2) ]; then \
	rm -f $(1); \
else \
	echo "Elevated permissions required — using sudo"; \
	sudo rm -f $(1); \
fi
endef

# $(call sudo-link,<src>,<dest>,<destdir>)
define sudo-link
@if [ -w $(3) ]; then \
	install -d $(3); \
	ln -sfn $(1) $(2); \
else \
	echo "Elevated permissions required — using sudo"; \
	sudo install -d $(3); \
	sudo ln -sfn $(1) $(2); \
fi
endef

# ============================================================================
# Main Targets
# ============================================================================

.PHONY: all build clean test lint format prep watch

## Build all artifacts
all: build

## Build the executable
build: $(SRC)
	@echo "Building $(BINARY)..."
	@mkdir -p $(dir $(BUILD_BIN))
	@$(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_BIN) $(MAIN_PKG)
	@echo "Built $(BUILD_BIN)"

## Run all tests with coverage
test:
	@echo "Running tests with coverage..."
	@mkdir -p $(OUT_DIR)/coverage
	@$(GO) test ./... -coverpkg=./cmd/...,./internal/... -coverprofile=$(OUT_DIR)/coverage/coverage.out
	@$(GO) tool cover -func=$(OUT_DIR)/coverage/coverage.out | tail -n 1
	@$(GO) tool gcov2lcov -infile $(OUT_DIR)/coverage/coverage.out -outfile $(OUT_DIR)/coverage/lcov.info >/dev/null
	@$(GO) tool cover -html=$(OUT_DIR)/coverage/coverage.out -o $(OUT_DIR)/coverage/index.html
	@echo "Coverage (LCOV): $(OUT_DIR)/coverage/lcov.info"
	@echo "Coverage (HTML): $(OUT_DIR)/coverage/index.html"

## Run golangci-lint
lint:
	@echo "Running golangci-lint..."
	@$(GO) tool golangci-lint run

## Format all Go files
format:
	@echo "Formatting Go files..."
	@gofmt -w $(SRC)
	@echo "Regenerating code..."
	@$(GO) generate ./...

## Prepare the codebase for a new commit
prep: format
	@echo "Tidying go.mod/go.sum..."
	@$(GO) mod tidy

## Watch source files and rebuild on changes
watch:
	@cd "$$(pwd -P)" && $(GO) tool air

## Remove all build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(OUT_DIR)

# ============================================================================
# Dependencies
# ============================================================================

.PHONY: deps

## Install Go module and tooling dependencies
deps:
	@if ! command -v $(GIT_CLIFF) >/dev/null 2>&1; then \
		echo "Installing git-cliff (requires cargo)..."; \
		if command -v cargo >/dev/null 2>&1; then \
			cargo install git-cliff >/dev/null 2>&1 || { echo "WARN: Failed to install git-cliff via cargo"; exit 1; }; \
		else \
			echo "WARN: cargo not found — install git-cliff manually from https://github.com/orhun/git-cliff"; \
		fi; \
	else \
		echo "INFO: git-cliff already installed ($$(command -v $(GIT_CLIFF)))"; \
	fi
	@echo "Downloading Go module dependencies..."
	@$(GO) mod download

# ============================================================================
# Changelog & Release Notes
# ============================================================================

.PHONY: changelog releasenotes

## Generate CHANGELOG.md from conventional commits
changelog:
	@if ! command -v $(GIT_CLIFF) >/dev/null 2>&1; then \
		echo "ERROR: git-cliff not found. Run 'make deps' or install git-cliff manually."; \
		exit 1; \
	fi
	@echo "Generating CHANGELOG.md via git-cliff..."
	@if [ -n "$(CHANGELOG_VERSION)" ]; then \
		$(GIT_CLIFF) --config cliff.toml --tag "$(CHANGELOG_VERSION)" --output CHANGELOG.md; \
	else \
		$(GIT_CLIFF) --config cliff.toml --output CHANGELOG.md; \
	fi

## Print the release notes snippet used by GoReleaser
releasenotes:
	@if [ ! -x "$(CURDIR)/scripts/releasenotes.sh" ]; then \
		echo "ERROR: Missing $(CURDIR)/scripts/releasenotes.sh" >&2; \
		exit 1; \
	fi
	@"$(CURDIR)/scripts/releasenotes.sh"

# ============================================================================
# Versioning
# ============================================================================

.PHONY: version version/bump_type version/github_actions

## Print the next semantic version derived from conventional commits
version:
	@if ! command -v $(GIT_CLIFF) >/dev/null 2>&1; then \
		echo "ERROR: git-cliff not found. Run 'make deps' or install git-cliff manually." >&2; \
		exit 1; \
	fi
	@next_version=$$($(GIT_CLIFF) --config cliff.toml --bumped-version 2>/dev/null); \
	if [ -z "$$next_version" ]; then \
		echo "ERROR: Unable to determine next version" >&2; \
		exit 1; \
	fi; \
	echo "$$next_version"

## Determine whether the upcoming version is a major/minor/patch bump
version/bump_type:
	@./scripts/bump_type.sh

version/github_actions:
	@output_file="$$GITHUB_OUTPUT"; \
	if [ -z "$$output_file" ]; then \
		echo "ERROR: GITHUB_OUTPUT is not set" >&2; \
		exit 1; \
	fi; \
	set -euo pipefail; \
	version=$$($(MAKE) --no-print-directory version); \
	bump=$$($(MAKE) --no-print-directory version/bump_type); \
	echo "next_version=$$version" >> "$$output_file"; \
	echo "bump_type=$$bump" >> "$$output_file"

# ============================================================================
# Release
# ============================================================================

.PHONY: release/commit release/tag

## Commit release artifacts (CHANGELOG, manpage, etc.)
release/commit:
	@if [ -z "$(RELEASE_VERSION)" ]; then \
		echo "ERROR: RELEASE_VERSION is required" >&2; \
		exit 1; \
	fi
	@if [ -z "$(RELEASE_BUMP_TYPE)" ]; then \
		echo "ERROR: RELEASE_BUMP_TYPE is required" >&2; \
		exit 1; \
	fi
	@mkdir -p $(dir $(RELEASE_COMMIT_FLAG))
	@rm -f $(RELEASE_COMMIT_FLAG)
	@git add -A; \
	if git diff --cached --quiet; then \
		echo "INFO: No release changes to commit"; \
	else \
		git commit -m "release($(RELEASE_BUMP_TYPE)): $(RELEASE_VERSION)"; \
		echo "true" > $(RELEASE_COMMIT_FLAG); \
	fi

## Tag the release
release/tag:
	@if [ -z "$(RELEASE_VERSION)" ]; then \
		echo "ERROR: RELEASE_VERSION is required" >&2; \
		exit 1; \
	fi
	@if git rev-parse "$(RELEASE_VERSION)" >/dev/null 2>&1; then \
		echo "INFO: Tag $(RELEASE_VERSION) already exists"; \
		git tag -d "$(RELEASE_VERSION)" >/dev/null 2>&1 || true; \
	fi
	@git tag -a "$(RELEASE_VERSION)" -m "Release $(RELEASE_VERSION)"

# ============================================================================
# Contrib Assets
# ============================================================================

.PHONY: man completions/generate

## Generate the man page
man:
	@echo "Generating man page..."
	@MAN_OUT_DIR=$(dir $(MANPAGE_SRC)) $(GO) run ./tools/gen-man

## Generate shell completions
completions/generate:
	@echo "Generating shell completions..."
	@$(GO) run ./tools/gen-completions

# ============================================================================
# Install
# ============================================================================

.PHONY: install install/all install/binary install/completions/all \
	install/completions/zsh install/completions/bash install/completions/fish \
	install/completions/oh-my-zsh install/man

## Install just the binary
install: install/binary

## Install binary, completions, and man page
install/all: install/binary install/completions/all install/man

install/binary: build
	@echo "Installing binary -> $(INSTALL_BIN)"
	$(call sudo-install,$(BUILD_BIN),$(INSTALL_BIN),$(INSTALL_BIN_DIR),755)
	@echo "Binary installed"

install/completions/all: install/completions/zsh install/completions/bash install/completions/fish install/completions/oh-my-zsh

install/completions/zsh:
	@echo "Installing Zsh completion assets into $(ZSH_DIR)"
	@mkdir -p $(ZSH_DIR)
	@if [ -f $(ZSH_SCRIPT_SRC) ]; then \
		install -m644 $(ZSH_SCRIPT_SRC) $(INSTALL_ZSH); \
	else \
		echo "WARN: Missing $(ZSH_SCRIPT_SRC); skipping bosun.zsh"; \
	fi
	@echo "NOTE: Source $$HOME/.zsh/$(ZSH_SCRIPT_NAME) from ~/.zshrc"

install/completions/bash:
	@echo "Installing Bash completion -> $(INSTALL_BASH)"
	@mkdir -p $(BASH_DIR)
	@if [ -f $(BASH_SCRIPT_SRC) ]; then \
		install -m644 $(BASH_SCRIPT_SRC) $(INSTALL_BASH); \
	else \
		echo "WARN: Missing $(BASH_SCRIPT_SRC); skipping Bash completion"; \
	fi
	@echo "NOTE: Add [[ -r $$HOME/.bash_completion.d/$(BASH_SCRIPT_NAME) ]] && . $$HOME/.bash_completion.d/$(BASH_SCRIPT_NAME) to ~/.bashrc"

install/completions/fish:
	@echo "Installing Fish completion -> $(INSTALL_FISH)"
	@mkdir -p $(FISH_DIR)
	@if [ -f $(FISH_SCRIPT_SRC) ]; then \
		install -m644 $(FISH_SCRIPT_SRC) $(INSTALL_FISH); \
	else \
		echo "WARN: Missing $(FISH_SCRIPT_SRC); skipping Fish completion"; \
	fi
	@echo "NOTE: Fish auto-loads $$HOME/.config/fish/completions/$(FISH_SCRIPT_NAME)"

install/completions/oh-my-zsh:
	@if [ -f $(OMZ_PLUGIN_SRC) ]; then \
		echo "Installing Oh-My-Zsh plugin -> $(OMZ_PLUGIN_DEST)"; \
		mkdir -p $(OMZ_PLUGIN_DIR); \
		install -m644 $(OMZ_PLUGIN_SRC) $(OMZ_PLUGIN_DEST); \
	else \
		echo "WARN: Missing $(OMZ_PLUGIN_SRC); skipping Oh-My-Zsh plugin"; \
	fi
	@echo "NOTE: Add 'bosun' to the plugins list in ~/.zshrc"

## Install the man page
install/man: man
	@echo "Installing man page -> $(INSTALL_MAN)"
	$(call sudo-install,$(MANPAGE_SRC),$(INSTALL_MAN),$(MANDIR),644)
	@echo "NOTE: View it via 'man bosun'"

# ============================================================================
# Link (development)
# ============================================================================

.PHONY: link

## Symlink every artifact (binary + all completions) back to the repo
link: build
	@echo "Linking binary -> $(INSTALL_BIN)"
	$(call sudo-link,$(CURDIR)/$(BUILD_BIN),$(INSTALL_BIN),$(INSTALL_BIN_DIR))
	@echo "Linking Zsh completion -> $(INSTALL_ZSH)"
	@install -d $(INSTALL_ZSH_DIR)
	@ln -sfn "$(CURDIR)/$(ZSH_SCRIPT_SRC)" $(INSTALL_ZSH)
	@echo "Linking Bash completion -> $(INSTALL_BASH)"
	@install -d $(INSTALL_BASH_DIR)
	@ln -sfn "$(CURDIR)/$(BASH_SCRIPT_SRC)" $(INSTALL_BASH)
	@echo "Linking Fish completion -> $(INSTALL_FISH)"
	@install -d $(INSTALL_FISH_DIR)
	@ln -sfn "$(CURDIR)/$(FISH_SCRIPT_SRC)" $(INSTALL_FISH)
	@echo "Linking Oh-My-Zsh plugin -> $(OMZ_PLUGIN_DEST)"
	@install -d $(OMZ_PLUGIN_DIR)
	@ln -sfn "$(CURDIR)/$(ZSH_SCRIPT_SRC)" $(OMZ_PLUGIN_DEST)
	@echo "Linking man page -> $(INSTALL_MAN)"
	$(call sudo-link,$(CURDIR)/$(MANPAGE_SRC),$(INSTALL_MAN),$(MANDIR))
	@echo "Linked all artifacts (remember to source ~/.zsh/bosun.zsh or add 'bosun' to OMZ plugins)"

# ============================================================================
# Uninstall
# ============================================================================

.PHONY: uninstall uninstall/all uninstall/binary uninstall/completions/zsh \
	uninstall/completions/bash uninstall/completions/fish \
	uninstall/completions/oh-my-zsh uninstall/man

## Remove the installed binary
uninstall: uninstall/binary

## Remove all installed artifacts
uninstall/all: uninstall/binary uninstall/completions/zsh uninstall/completions/bash uninstall/completions/fish uninstall/completions/oh-my-zsh uninstall/man

uninstall/binary:
	@echo "Removing binary $(INSTALL_BIN)"
	$(call sudo-remove,$(INSTALL_BIN),$(INSTALL_BIN_DIR))

uninstall/completions/zsh:
	@echo "Removing Zsh completion"
	$(call sudo-remove,$(INSTALL_ZSH),$(INSTALL_ZSH_DIR))

uninstall/completions/bash:
	@echo "Removing Bash completion"
	$(call sudo-remove,$(INSTALL_BASH),$(INSTALL_BASH_DIR))

uninstall/completions/fish:
	@echo "Removing Fish completion"
	$(call sudo-remove,$(INSTALL_FISH),$(INSTALL_FISH_DIR))

uninstall/completions/oh-my-zsh:
	@echo "Removing Oh-My-Zsh plugin"
	$(call sudo-remove,$(OMZ_PLUGIN_DEST),$(OMZ_PLUGIN_DIR))

uninstall/man:
	@echo "Removing man page $(INSTALL_MAN)"
	$(call sudo-remove,$(INSTALL_MAN),$(MANDIR))

# ============================================================================
# Distribution & Publishing
# ============================================================================

.PHONY: dist release publish/homebrew publish/macports

## Build release artifacts locally (snapshot)
dist:
	@echo "Building release artifacts via GoReleaser..."
	@notes="$$($(MAKE) --no-print-directory releasenotes)"; \
	GIT_CLIFF_RELEASE_NOTES="$$notes" \
		$(GO) tool goreleaser release --snapshot --clean

## Build and publish release artifacts
release:
	@echo "Building release artifacts via GoReleaser..."
	@notes="$$($(MAKE) --no-print-directory releasenotes)"; \
	GIT_CLIFF_RELEASE_NOTES="$$notes" \
		$(GO) tool goreleaser release --clean

## Render and publish the Homebrew formula to the tap repository
publish/homebrew:
	@./scripts/publish_homebrew.sh "$(TAG)" "$(TAP_REPO)" "$(TAP_FORMULA_PATH)" "$(TAP_FORMULA)"

## Render and publish the MacPorts Portfile to the ports repository
publish/macports:
	@PORT_PULLREQUEST="$(PORT_PULLREQUEST)" ./scripts/publish_macports.sh "$(TAG)" "$(PORT_REPO)" "$(PORTFILE_PATH)" "$(PORTFILE)"

# ============================================================================
# Test (Completions)
# ============================================================================

.PHONY: test/completions test/completions/bash test/completions/zsh test/completions/fish

## Run all completion smoke tests
test/completions: test/completions/bash test/completions/zsh test/completions/fish

## Run Bash completion smoke test
test/completions/bash:
	@bash contrib/completions/bash/bosun.test.bash

## Run Zsh completion smoke test
test/completions/zsh:
	@zsh contrib/completions/zsh/bosun.test.zsh

## Run Fish completion smoke test
test/completions/fish:
	@fish contrib/completions/fish/bosun.test.fish

# ============================================================================
# Utils
# ============================================================================

.PHONY: help vars _print-var

## This help screen
help:
	@printf "Available targets:\n\n"
	@awk '/^[a-zA-Z\-\_0-9%:\\\/]+/ { \
		helpMessage = match(lastLine, /^## (.*)/); \
		if (helpMessage) { \
			helpCommand = $$1; \
			helpMessage = substr(lastLine, RSTART + 3, RLENGTH); \
			gsub("\\\\", "", helpCommand); \
			gsub(":+$$", "", helpCommand); \
			printf "  \x1b[32;01m%-35s\x1b[0m %s\n", helpCommand, helpMessage; \
		} \
	} \
	{ lastLine = $$0 }' $(MAKEFILE_LIST) | sort -u
	@printf "\n"

## Show the variables used in the Makefile and their values
vars:
	@printf "Variable values:\n\n"
	@awk 'BEGIN { FS = "[:?]?="; } /^[A-Za-z0-9_]+[[:space:]]*[:?]?=/ { \
		if ($$0 ~ /\?=/) operator = "?="; \
		else if ($$0 ~ /:=/) operator = ":="; \
		else operator = "="; \
		print $$1, operator; \
	}' $(MAKEFILE_LIST) | \
	while read var op; do \
		value=$$(make --no-print-directory -f $(MAKEFILE_LIST) _print-var VAR=$$var); \
		printf "  \x1b[32;01m%-35s\x1b[0m%2s \x1b[34;01m%s\x1b[0m\n" "$$var" "$$op" "$$value"; \
	done
	@printf "\n"

_print-var:
	@echo $($(VAR))
