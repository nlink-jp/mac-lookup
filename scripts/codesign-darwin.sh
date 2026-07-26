#!/bin/sh
# codesign-darwin.sh — sign a darwin Mach-O binary with a Developer ID
# Application identity (Hardened Runtime + Apple timestamp), or skip
# gracefully if codesigning is not possible.
#
# Usage:
#   codesign-darwin.sh <binary> [identity] [identifier]
#
# Identity defaults to "Developer ID Application" — matches any Developer
# ID Application certificate in the user's keychain. Override via env or
# 2nd argument when more than one Developer ID identity is present.
# Identifier (3rd arg) sets the code-signature identifier (-i). Pass the
# canonical tool name so it stays stable even when the built file carries
# an -<os>-<arch> suffix that is renamed away at package time. Defaults to
# codesign's filename-derived value when omitted.
#
# Behaviour:
#   - Skips silently on non-macOS hosts (cross-compile from Linux/etc.)
#   - Skips with a one-line warning if no matching identity exists
#     (contributors without an Apple Developer Program account can still
#     build; the binary keeps its Go-linker ad-hoc signature)
#   - Skips silently if the target file is not Mach-O (Linux/Windows
#     binaries fall through untouched)
#
# Why --options runtime + --timestamp:
#   Apple's notary service rejects binaries that lack Hardened Runtime
#   or an Apple-issued secure timestamp. Setting both at sign time keeps
#   `make package` notarize-ready without a second resign pass.

set -e

BINARY="${1:?Usage: $0 <binary> [identity] [identifier]}"
IDENTITY="${2:-${CODESIGN_IDENTITY:-Developer ID Application}}"
IDENTIFIER="${3:-}"

# Cross-compile from non-Darwin: nothing to do
if [ "$(uname)" != "Darwin" ]; then
  exit 0
fi

if [ ! -f "$BINARY" ]; then
  echo "[codesign] $BINARY not found, skipping" >&2
  exit 0
fi

# Non-Mach-O target (e.g. Linux/Windows binary produced by cross-compile)
if ! file "$BINARY" | grep -q 'Mach-O'; then
  exit 0
fi

# No Developer ID identity in keychain — keep the linker ad-hoc signature
if ! security find-identity -v -p codesigning 2>/dev/null | grep -q "$IDENTITY"; then
  echo "[codesign] No '$IDENTITY' identity in keychain; $BINARY keeps the Go-linker ad-hoc signature" >&2
  exit 0
fi

if [ -n "$IDENTIFIER" ]; then
  codesign --force --options runtime --timestamp -i "$IDENTIFIER" --sign "$IDENTITY" "$BINARY"
else
  codesign --force --options runtime --timestamp --sign "$IDENTITY" "$BINARY"
fi
echo "[codesign] Signed $BINARY with '$IDENTITY'"
