# release-brew.mk — shared Homebrew tap generation hook (nlink-jp).
#
# Canonical copy: nlink-jp/.github templates/release-brew.mk. Vendor it into a
# repo's scripts/ alongside gen-brew.sh + the matching <kind>.rb.tmpl, then in
# the repo Makefile define a few vars and `include` this fragment:
#
#     # formula (Go CLI):
#     BREW_KIND := formula
#     BREW_DESC := One-line description of the tool
#     include scripts/release-brew.mk
#
#     # cask (notarized GUI .app):
#     BREW_KIND      := cask
#     BREW_DESC      := One-line description of the app
#     BREW_NAME      := $(APP)               # if the repo uses APP, not BINARY
#     BREW_APP       := $(APP).app
#     BREW_BUNDLE_ID := com.example.name
#     include scripts/release-brew.mk
#
# Then, after `make package` (which produces the signed + notarized zip):
#
#     make brew          # generate + commit into the tap (+ push if it has a remote)
#     make brew-print    # render to stdout only (no tap checkout needed)
#
# The package target itself is never modified: this only adds new targets, so
# the release build stays byte-for-byte the same.

BREW_TAP_DIR ?= $(HOME)/works/nlink-jp/homebrew-tap
GEN_BREW     ?= scripts/gen-brew.sh
DIST_DIR     ?= dist
BREW_NAME    ?= $(BINARY)
BREW_ZIP     ?= $(DIST_DIR)/$(BREW_NAME)-$(VERSION)-darwin-arm64.zip

BREW_ENV = BREW_KIND="$(BREW_KIND)" BREW_DESC="$(BREW_DESC)" \
           BREW_APP="$(BREW_APP)" BREW_BUNDLE_ID="$(BREW_BUNDLE_ID)" \
           BREW_REPO="$(BREW_REPO)" BREW_TAP_DIR="$(BREW_TAP_DIR)"

.PHONY: brew brew-print

## brew: generate this repo's formula/cask from the built darwin-arm64 zip and
## write it into the local homebrew-tap (commits; pushes if the tap has a remote).
brew:
	@$(BREW_ENV) $(GEN_BREW) "$(BREW_ZIP)"

## brew-print: render the formula/cask to stdout without touching the tap.
brew-print:
	@$(BREW_ENV) $(GEN_BREW) --print "$(BREW_ZIP)"
