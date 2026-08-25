#!/bin/sh
# notarize-darwin.sh — submit a zip-packaged darwin binary to Apple's
# notary service and wait for the verdict.
#
# Usage:
#   notarize-darwin.sh <zip-path> [profile-name]
#
# Profile name defaults to the value of NOTARY_PROFILE env, then to
# `nlink-jp-notary`. Credentials are stored once per machine via:
#
#   xcrun notarytool store-credentials nlink-jp-notary \
#       --key <p8>  --key-id <id>  --issuer <uuid>
#
# Behaviour:
#   - Skips on non-Darwin hosts (cross-compile from Linux/etc.)
#   - When the keychain profile cannot be used, the actual notarytool
#     error is printed and the zip ships un-notarised (exit 0) so other
#     contributors / CI without credentials still produce the zip. This
#     distinguishes a genuinely missing profile from a real error such
#     as an expired Apple Developer agreement (HTTP 403).
#   - On submission failure, prints the Apple-returned log and exits
#     non-zero so a release pipeline can stop.
#   - On Acceptance a `<zip>.notarized` marker file is written beside
#     the zip; `make verify-release` gates on it (any earlier marker is
#     cleared at startup), so the skip paths above cannot reach a
#     release unnoticed.
#
# Why we don't staple: notarisation of bare CLI binaries inside a
# zip cannot be stapled — `stapler staple` only works on app
# bundles, dmgs, and pkgs. The notarisation ticket lives on Apple's
# servers and macOS checks it online the first time the binary is
# launched on a given machine. This is the standard pattern for
# non-bundle distributables (cf. the official notarytool docs).

set -e

ZIP="${1:?Usage: $0 <zip-path> [profile]}"
PROFILE="${2:-${NOTARY_PROFILE:-nlink-jp-notary}}"

if [ "$(uname)" != "Darwin" ]; then
  exit 0
fi

if [ ! -f "$ZIP" ]; then
  echo "[notarize] $ZIP not found, skipping" >&2
  exit 0
fi

# Clear any marker from a previous run first, so no exit path below can
# leave a stale "notarized" verdict standing next to a rebuilt zip.
rm -f "$ZIP.notarized"

# Probe the keychain profile cheaply (notarytool has no dedicated
# "is profile present" command). `history` returns quickly without
# uploading anything, so we use it as a liveness check. Capture its
# output so a real failure (expired Apple agreement, auth, transient
# network) is surfaced instead of being misreported as "profile not
# found" — the old behaviour hid 403 "required agreement has expired"
# errors behind a misleading message.
if ! PROBE_OUT=$(xcrun notarytool history --keychain-profile "$PROFILE" 2>&1); then
  echo "[notarize] Cannot use keychain profile '$PROFILE'; $ZIP will ship un-notarised." >&2
  echo "[notarize] notarytool reported:" >&2
  printf '%s\n' "$PROBE_OUT" | sed 's/^/[notarize]   /' >&2
  # The credential lives in the data-protection keychain, which is
  # unreadable while the screen is locked — the profile then *looks*
  # deleted (measured 2026-08: an unattended release batch died this
  # way mid-run). Diagnose that first; re-registering is never the fix.
  if ioreg -n Root -d1 2>/dev/null | grep -q '"IOConsoleLocked" = Yes'; then
    echo "[notarize] --> The screen is locked: data-protection keychain items are" >&2
    echo "[notarize]     unavailable while locked. Unlock the screen and re-run;" >&2
    echo "[notarize]     the credential is intact." >&2
  else
    case "$PROBE_OUT" in
      *403*|*[Aa]greement*)
        echo "[notarize] --> Apple Developer agreement issue, not a missing key." >&2
        echo "[notarize]     Sign the updated agreement at https://developer.apple.com/account/" >&2
        echo "[notarize]     (Account Holder), wait a few minutes, then retry." >&2
        ;;
      *)
        echo "[notarize] --> If the profile is not set up on this machine, run once:" >&2
        echo "[notarize]       xcrun notarytool store-credentials $PROFILE --key <p8> --key-id <id> --issuer <uuid>" >&2
        ;;
    esac
  fi
  exit 0
fi

echo "[notarize] Submitting $ZIP to Apple notary service (this typically takes 30s-2m)..."
SUBMISSION_OUT=$(xcrun notarytool submit "$ZIP" --keychain-profile "$PROFILE" --wait 2>&1) || {
  echo "[notarize] $ZIP: submission failed" >&2
  echo "$SUBMISSION_OUT" >&2
  exit 1
}

echo "$SUBMISSION_OUT"

# notarytool exits 0 on Accepted, non-zero otherwise. As an extra
# guard, parse the status line in the output and fail explicitly
# on anything other than "Accepted" so a release pipeline halts
# even if Apple shifts exit-code semantics in a future release.
if printf '%s\n' "$SUBMISSION_OUT" | grep -q 'status: Accepted'; then
  echo "[notarize] $ZIP: Accepted"
  # Success marker for verify-release. The fail-open above (shipping
  # un-notarised when the profile probe fails) exists for contributors
  # without credentials — but on the release machine it once shipped an
  # un-notarised zip while verify-release stayed green, because the
  # probe failed on an updated Apple agreement and nothing downstream
  # checked. verify-release now requires this marker, so the fail-open
  # path can no longer reach a release unnoticed.
  touch "$ZIP.notarized"
  exit 0
fi

echo "[notarize] $ZIP: notarisation did not succeed (see status above)" >&2
exit 1
