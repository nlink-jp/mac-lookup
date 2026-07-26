# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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
