#!/bin/sh
# gen-brew.sh — generate this repo's Homebrew formula/cask from its just-built
# release asset and write it into the local nlink-jp/homebrew-tap checkout.
#
# Vendored per-repo alongside codesign-darwin.sh / notarize-darwin.sh (canonical
# copy lives in nlink-jp/.github templates/). Run as the last step of a release,
# after `make package` has produced the signed + notarized darwin-arm64 zip.
#
# Usage:
#   gen-brew.sh [--print | --no-push] [--tap-dir DIR] <release-zip>
#
#   <release-zip>  Path to <name>-v<version>-darwin-arm64.zip (the real
#                  build artifact). name and version are parsed from it, so
#                  the sha256/version can never drift from what shipped.
#   --print        Render to stdout only; do not touch the tap. (No tap needed.)
#   --no-push      Write + commit into the tap, but do not push.
#   --tap-dir DIR  Override BREW_TAP_DIR.
#
# Configuration (env, usually set by release-brew.mk from Makefile vars):
#   BREW_KIND       formula | cask                             (required)
#   BREW_DESC       one-line desc for the formula/cask         (required)
#   BREW_TAP_DIR    local homebrew-tap checkout                (required unless --print)
#   BREW_APP        <Name>.app                                 (cask only)
#   BREW_BUNDLE_ID  bundle id for the zap stanza               (cask only)
#   BREW_REPO       GitHub repo slug, if it differs from the tool/binary name
#                   parsed from the asset (e.g. repo markdown-viewer ships mdv);
#                   default: the parsed name.
#   BREW_TEMPLATES_DIR  dir holding formula.rb.tmpl/cask.rb.tmpl
#                       (default: this script's directory)
#
# Notes:
#   - arm64-only, prebuilt: the tap installs the notarized asset as-is so the
#     Developer ID signature is preserved (verified via `spctl -a`). Never a
#     source build.
#   - If the tap has no `origin` remote yet (not published), commit succeeds
#     and push is skipped with a notice — the same command pushes once a
#     remote exists.

set -e

PRINT=0
NO_PUSH=0
TAP_DIR="${BREW_TAP_DIR:-}"
ZIP=""

die() { echo "[gen-brew] $1" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --print)    PRINT=1 ;;
    --no-push)  NO_PUSH=1 ;;
    --tap-dir)  shift; TAP_DIR="${1:?--tap-dir needs an argument}" ;;
    -h|--help)  sed -n '2,40p' "$0"; exit 0 ;;
    -*)         die "unknown option: $1" ;;
    *)          [ -z "$ZIP" ] || die "unexpected extra argument: $1"; ZIP="$1" ;;
  esac
  shift
done

[ -n "$ZIP" ] || die "Usage: $0 [--print|--no-push] [--tap-dir DIR] <release-zip>"
[ -f "$ZIP" ] || die "release zip not found: $ZIP"

KIND="${BREW_KIND:?BREW_KIND must be 'formula' or 'cask'}"
case "$KIND" in
  formula|cask) ;;
  *) die "BREW_KIND must be 'formula' or 'cask' (got '$KIND')" ;;
esac
DESC="${BREW_DESC:?BREW_DESC (one-line description) is required}"

# Parse name + version from the artifact filename — the single source of truth.
BASE=$(basename "$ZIP")
case "$BASE" in
  *-darwin-arm64.zip) ;;
  *) die "asset must be named <name>-v<version>-darwin-arm64.zip (got '$BASE')" ;;
esac
STEM=${BASE%-darwin-arm64.zip}      # <name>-v<version>
NAME=${STEM%-v*}                    # <name>
VERSION=${STEM##*-v}                # <version>
case "$STEM" in
  *-v*) ;;
  *) die "cannot parse version from '$BASE' (expected <name>-v<version>-...)" ;;
esac
[ -n "$NAME" ] && [ -n "$VERSION" ] || die "failed to parse name/version from '$BASE'"

# GitHub repo slug — defaults to the tool name, overridable when they differ
# (e.g. the markdown-viewer repo ships a binary/asset named mdv).
REPO="${BREW_REPO:-$NAME}"

# Formula class name: kebab -> UpperCamel (matches Homebrew's Formulary.class_s
# for our lowercase-kebab tool names).
CLASS=$(printf '%s\n' "$NAME" | awk -F- '{s="";for(i=1;i<=NF;i++){s=s toupper(substr($i,1,1)) substr($i,2)} print s}')

# sha256 of the real artifact.
if command -v shasum >/dev/null 2>&1; then
  SHA256=$(shasum -a 256 "$ZIP" | awk '{print $1}')
elif command -v sha256sum >/dev/null 2>&1; then
  SHA256=$(sha256sum "$ZIP" | awk '{print $1}')
else
  die "need shasum or sha256sum to compute the checksum"
fi
[ -n "$SHA256" ] || die "failed to compute sha256 for $ZIP"

APP="${BREW_APP:-}"
BUNDLE_ID="${BREW_BUNDLE_ID:-}"
if [ "$KIND" = cask ]; then
  [ -n "$APP" ] || die "BREW_APP (<Name>.app) is required for casks"
  [ -n "$BUNDLE_ID" ] || die "BREW_BUNDLE_ID is required for casks"
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TPL_DIR="${BREW_TEMPLATES_DIR:-$SCRIPT_DIR}"
TPL="$TPL_DIR/$KIND.rb.tmpl"
[ -f "$TPL" ] || die "template not found: $TPL"

# Escape a value for safe use on the replacement side of sed s|...|...| .
esc_repl() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

render() {
  sed \
    -e "s|@CLASS@|$(esc_repl "$CLASS")|g" \
    -e "s|@NAME@|$(esc_repl "$NAME")|g" \
    -e "s|@REPO@|$(esc_repl "$REPO")|g" \
    -e "s|@DESC@|$(esc_repl "$DESC")|g" \
    -e "s|@VERSION@|$(esc_repl "$VERSION")|g" \
    -e "s|@SHA256@|$(esc_repl "$SHA256")|g" \
    -e "s|@APP@|$(esc_repl "$APP")|g" \
    -e "s|@BUNDLE_ID@|$(esc_repl "$BUNDLE_ID")|g" \
    "$TPL"
}

if [ "$PRINT" -eq 1 ]; then
  render
  exit 0
fi

[ -n "$TAP_DIR" ] || die "BREW_TAP_DIR (or --tap-dir) is required (no implicit clone)"
[ -d "$TAP_DIR" ] || die "tap checkout not found: $TAP_DIR"
[ -d "$TAP_DIR/.git" ] || die "not a git repo: $TAP_DIR"

case "$KIND" in
  formula) SUBDIR=Formula ;;
  cask)    SUBDIR=Casks ;;
esac
mkdir -p "$TAP_DIR/$SUBDIR"
REL="$SUBDIR/$NAME.rb"
render > "$TAP_DIR/$REL"
echo "[gen-brew] wrote $REL ($KIND, v$VERSION, sha256 ${SHA256%${SHA256#??????????}}…)"

git -C "$TAP_DIR" add "$REL"
if git -C "$TAP_DIR" diff --cached --quiet -- "$REL"; then
  echo "[gen-brew] $REL already up to date; nothing to commit"
  exit 0
fi
git -C "$TAP_DIR" commit -q -m "chore: $NAME $VERSION ($KIND)"
echo "[gen-brew] committed $REL in $TAP_DIR"

if [ "$NO_PUSH" -eq 1 ]; then
  echo "[gen-brew] --no-push: leaving commit unpushed"
  exit 0
fi
if ! REMOTE=$(git -C "$TAP_DIR" remote get-url origin 2>/dev/null); then
  echo "[gen-brew] tap has no 'origin' remote yet; commit left local (publish the tap, then re-run to push)" >&2
  exit 0
fi
BRANCH=$(git -C "$TAP_DIR" rev-parse --abbrev-ref HEAD)
echo "[gen-brew] pushing to $REMOTE ($BRANCH)..."
git -C "$TAP_DIR" pull --rebase --autostash origin "$BRANCH" >/dev/null 2>&1 || true
git -C "$TAP_DIR" push origin "$BRANCH"
echo "[gen-brew] pushed $REL"
