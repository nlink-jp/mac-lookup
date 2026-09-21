# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).

## [0.3.0] - 2026-09-21

### Changed

- **An MCP tool call carrying an argument the tool does not declare now fails
  instead of being quietly ignored.** This is a deliberate behaviour change,
  required by org ADR-021 §4. Until now a misspelt argument was dropped and the
  call ran without it, which was worst on `search_vendor`: send `offest`
  instead of `offset` and every page of a broad registrant search comes back as
  the first 50 matches, so a loop that walks `has_more` repeats page one forever
  and never sees the rest. Every tool — including `get_usage`, `update_db` and
  `db_status`, which take no arguments — now decodes with
  `DisallowUnknownFields` and refuses the call, naming the offending field:
  `arguments: json: unknown field "offest"`.

  A malformed argument object is refused for the same reason. The decode error
  used to be discarded along with the unknown field, so `{"macs": "286FB9…"}` —
  one address where an array was expected — ran as if no address had been
  supplied and came back with "provide 'mac' … or 'macs'", an answer that
  contradicted the request. It now reports the type mismatch.

  Nothing runs before the arguments decode, so a rejected call reads no
  registry and downloads nothing. Omitting `arguments`, or sending `{}` or
  `null`, still means "no arguments" and is not an error. There is no
  compatibility shim: an argument name this server does not declare has never
  meant anything, so the only fix is to correct it.

### Fixed

- **Every MCP tool input schema is closed.** The schemas omitted
  `additionalProperties: false`, so a mistyped argument read as a legitimate one
  to any client that validates against them. Schemas are now built through a
  single `obj()` helper that sets the flag, and an arch test fails if a tool's
  schema omits it — org ADR-021 §10 requires the test as well as the flag,
  because a rule stated only in prose is re-decided by whoever adds the next
  tool.

## [0.2.1] - 2026-09-21

### Fixed

- A number in the config file was accepted when it was not one. `NaN` passed the
  range check — it fails every comparison, so "reject what is below the floor"
  lets it through — and `Inf` or `1e300` overflowed the duration it became.
  Ranges are now stated from the inside, with a ceiling.

## [0.2.0] - 2026-08-31

### Changed

- **`search_vendor` returns its matches inline.** The MCP tool no longer writes
  a JSON Lines file: matches come back in `matches`, bounded by `limit`
  (default 50, was "no limit") and walked with the new `offset`, with `has_more`
  saying whether any are left. `total` is still the true count.

  Migration: replace a `workspace_root` call plus a file read with a loop that
  advances `offset` by `limit` while `has_more` is true.

### Added

- `search_vendor` takes `offset`, and every result carries `offset`, `limit` and
  `has_more`.

### Removed

- `search_vendor`'s `workspace_root` argument and the `matches_file` /
  `written` / `truncated` / `note` / `format` result fields.
- The `[workspace]` config section and `MAC_LOOKUP_WORKSPACE`. The server has no
  output directory: it touches no filesystem, so it works unchanged against a
  client that has none.

## [0.1.0] - 2026-07-26

### Added

- Phase 1 (Planning): RFP in `docs/{en,ja}/`, covering the problem statement,
  command surface, resolution order, exit-code contract, series placement and
  the measured IEEE constraints.
- Phase 2 (Scaffolding): repository structure, Makefile (`build` → `dist/`,
  `build-all`, `package` with Developer ID signing + notarization, `brew`),
  `.gitignore`, MIT LICENSE, `config.example.toml`, and the release scripts.
- `lookup <MAC>...` — resolve MAC addresses, BSSIDs, or 24/28/36-bit prefixes,
  answered offline from the cached registries. Accepts colon, hyphen, dot, and
  bare-hex notations. A single positional address in text mode uses grep-style
  exit codes (`0` = named vendor, `1` = no name, `2` = error); multiple
  addresses, stdin, or `--json` switch to batch mode (per-address results on
  stdout, error-only exit code). `--json` emits JSON Lines.
- Address classification before any registry lookup: broadcast, then the I/G
  bit (multicast, named from a table of reserved protocol ranges), then the U/L
  bit. A locally administered address reports
  `vendor_lookup_applicable: false`, so a randomized MAC is never mistaken for
  a device that merely went unfound. Every result carries a `note` explaining
  why no vendor name is present.
- Longest-prefix matching (36 → 28 → 24 bits) across MA-L, MA-M, MA-S and IAB,
  because IEEE subdivides 24-bit blocks and several vendors can share one OUI.
  A `Private` registration (name withheld) and a block IEEE retains for
  subdivision are each reported distinctly, never as a vendor name.
- CID is indexed separately and consulted only for locally administered
  addresses — the registry that exists for exactly that purpose.
- `search <query>` — find a registrant's assignments by name substring, with
  `--limit` and an explicit notice when the list is truncated.
- `update` — conditional download (ETag / Last-Modified) of all five registry
  files, rebuilding the store atomically (temp + rename, deterministic sorted
  serialization). A registry that 304s or that fails to fetch keeps its cached
  entries, and the two cases are reported separately so "unchanged upstream" is
  never confused with "IEEE unreachable". Only MA-L is indispensable.
- `status` — sources, generation time, per-registry counts, and staleness.
- `mcp` — local stdio MCP server (JSON-RPC 2.0, standard library only) exposing
  `lookup_mac`, `search_vendor`, `db_status`, `update_db` and `get_usage`.
  `search_vendor` is file-mediated. `get_usage` returns an embedded manual,
  advertised via the initialize `instructions` field.
- Auto-refetch when the cache is older than the TTL (default 24h, floored at 6h
  out of fetch etiquette). A refetch failure falls back to the cached copy with
  a warning. Disable with `--no-update` or `[ieee] auto_update = false`.
- Configuration via sectioned TOML (`~/.config/mac-lookup/config.toml`) and
  `MAC_LOOKUP_*` environment variables (`BASE_URL`, `STORE`, `WORKSPACE`,
  `TTL_MINUTES`, `AUTO_UPDATE`). No credentials required.
- Fetch etiquette: an honest `User-Agent` naming the tool — the IEEE origin
  answers browser-like agents with HTTP 418, which is reported with its cause.
- Zero external dependencies (standard library only).
