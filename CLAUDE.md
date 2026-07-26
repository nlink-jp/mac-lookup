# CLAUDE.md — mac-lookup

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Purpose

CLI + local MCP server that resolves a **MAC address or BSSID** to its
manufacturer and to the nature of the address itself, answered offline from a
locally cached copy of the IEEE Registration Authority public registries
(MA-L / MA-M / MA-S / IAB / CID). The L2 counterpart to `asn-lookup` (L3
attribution) and a member of the offline `*-lookup` family alongside
`tor-exit-lookup` and `icloud-relay-lookup`.

## Build & test

```bash
make build       # Build → dist/mac-lookup  (never `go build` directly)
make test        # Tests with race detector + coverage
go test ./...    # Same without Makefile
```

## Architecture

```
main.go                 CLI entry: main.version → app.Run
internal/macaddr/       Parse + classify an address (cast, administration, well-known). Pure, no registry
internal/ouidb/         Parse the 5 registry CSVs, longest-prefix DB, deterministic store (pure)
internal/ieee/          Fetcher interface + HTTPFetcher (public endpoints, conditional GET, honest UA)
internal/config/        Sectioned-TOML subset + env/flag resolution (no credentials)
internal/engine/        LoadDB / Update / Lookup / SearchVendor / EnsureFresh / IsStale
internal/app/           Dispatch + lookup/search/update/status/mcp; --json, batch, grep-style exit codes
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server + tools (lookup_mac/search_vendor/db_status/update_db/get_usage)
```

Core logic takes io.Reader/io.Writer and injected dependencies for testability
(the fetcher is an interface, mocked in tests). **No external dependencies —
standard library only.** See [docs/en/architecture.md](docs/en/architecture.md)
for the "why".

## Key conventions

- **Classification precedes vendor lookup, always.** Broadcast → I/G bit
  (multicast, with the well-known table) → U/L bit (locally administered) →
  only then the registry. A locally administered address is reported as
  *vendor lookup does not apply*, never as "vendor unknown" — collapsing those
  two is what makes an investigator misidentify a device.
- **Longest-prefix match, 36 → 28 → 24.** MA-M and MA-S/IAB carve up 24-bit
  prefixes that MA-L also uses, so several vendors legitimately share one OUI.
  A first-six-digits lookup returns the wrong vendor.
- **`Private` is not "unassigned".** IEEE lets a registrant withhold their name;
  that is a registered assignment whose organization is undisclosed. Keep the
  distinction in the output.
- **The registrant address stays verbatim.** It is free text with inconsistent
  formatting, so no country code or other field is parsed out of it — the
  mis-extraction rate would exceed the value.
- **No credentials.** The IEEE files are public. There is no token or API key —
  unlike asn-lookup (ipinfo token) or abuse-lookup (AbuseIPDB key).
- **Grep-style exit codes** (`lookup`): `0` = named vendor, `1` = no name,
  `2` = error. Single positional address in text mode only; multiple addresses,
  stdin, or `--json` switch to batch (error-only `0`/`2`).
- **Deterministic store:** serialization takes an explicit `generatedUnix` and
  sorts entries; only `engine.Update` reads the clock. Writes are atomic
  (temp + rename) so a crash never leaves a truncated store.
- **Freshness lives in the record** (`generated_at`), not the file mtime.

## IEEE fetch etiquette (measured 2026-07-26)

- **Never spoof a browser User-Agent.** `User-Agent: Mozilla/5.0` is answered
  with `418 I'm a Teapot`; a plain client User-Agent gets `200`. Keep
  `ieee.UserAgent` honest.
- **Use conditional GET.** All five files return `ETag` and `Last-Modified`, so
  a no-op refresh costs a `304` instead of 5.6 MB.
- **6-hour TTL floor** (`config.MinTTL`). IEEE regenerates the files about once
  a day; polling faster only wastes their bandwidth.

## Status

Phase 1 (RFP) and Phase 2 (scaffold) complete, in `_wip/` — local only, not yet
pushed. The `lookup`/`search`/`update`/`status`/`mcp` commands are dispatched
but not implemented. Next: Phase 3 (core + features + release). No version tags.

## Communication Language

All communication between contributors and Claude Code is conducted in
**Japanese**.
