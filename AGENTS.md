# AGENTS.md — mac-lookup

## What this is

A CLI + local MCP server that answers two questions about a MAC address or
BSSID: **what kind of address is this**, and **who made it**. It resolves
offline from a locally cached copy of the five IEEE Registration Authority
registry files (MA-L, MA-M, MA-S, IAB, CID): `update` downloads them once, and
every `lookup` is an in-memory longest-prefix match. The L2 counterpart to
`asn-lookup` (L3 attribution), sharing the offline-cache shape of
`tor-exit-lookup`.

Module path: `github.com/nlink-jp/mac-lookup`.

## Build & test

```bash
make build      # → dist/mac-lookup  (NEVER `go build` directly)
make test       # go test -race -cover ./...
make check      # lint + test + build-all
make build-all  # cross-compile linux/{amd64,arm64}, darwin/arm64, windows/amd64
make package    # archive + Developer ID sign + notarize (darwin arm64)
make brew       # generate the tap formula from the packaged zip
```

Go 1.25+. **No external dependencies** — standard library only.

## Layout

```
main.go                 Entry point; sets main.version, calls app.Run.
internal/macaddr/       Address parsing + classification. Pure; knows nothing about the registries.
  macaddr.go            Cast / Administration / Address / the well-known multicast table.
internal/ouidb/         Registry parsing + longest-prefix DB + on-disk store.
  ouidb.go              Registry.Bits(), Entry (+Private()), DB, Source.
internal/ieee/          Fetcher interface + HTTPFetcher.
  fetch.go              UserAgent, DefaultBaseURL, RegistryFiles catalogue.
internal/config/        Sectioned-TOML subset parser + env/flag resolution (no secrets).
internal/engine/        Ties config+fetcher+store: LoadDB, Update, Lookup, SearchVendor, EnsureFresh, IsStale.
internal/app/           CLI: dispatch, lookup/search/update/status/mcp, output; grep-style + batch/JSON.
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server + tools.
  tools.go              Tool-name constants.
scripts/                codesign-darwin.sh, notarize-darwin.sh, gen-brew.sh, formula.rb.tmpl, release-brew.mk.
docs/{en,ja}/           RFP and architecture notes.
```

## Environment variables

All optional; every one overrides the config file. There are **no credentials**.

| Variable | Meaning |
|---|---|
| `MAC_LOOKUP_BASE_URL` | IEEE registry download origin |
| `MAC_LOOKUP_STORE` | Path to the cached registry store |
| `MAC_LOOKUP_WORKSPACE` | Default output directory for file-mediated MCP results |
| `MAC_LOOKUP_TTL_MINUTES` | Auto-refetch threshold (floored at 6h) |
| `MAC_LOOKUP_AUTO_UPDATE` | Whether `lookup` auto-refetches a stale cache |

## Key design decisions

- **Classification before vendor lookup.** The order — broadcast, then I/G bit
  (multicast), then U/L bit (locally administered), then the registry — is the
  product, not an implementation detail. See Gotchas.
- **Offline DB, not an online API.** Like asn-lookup, the whole dataset is
  downloaded once and queried locally. `lookup` never touches the network.
- **No credentials.** The IEEE files are public; nothing to configure or leak.
- **Engine is shared** by the CLI and the MCP server so their behaviour cannot
  diverge.
- **Fetcher is an interface** (`ieee.Fetcher`) so the engine is tested without
  touching the network.
- **Deterministic store.** Serialization takes an explicit `generatedUnix` and
  sorts entries, so identical input yields byte-identical output. Only
  `engine.Update` reads the clock. Writes are atomic (temp + rename).
- **Freshness lives in the record** (`generated_at`), not the file mtime, so it
  survives copies and backups.
- **One parser for five files.** All the registry CSVs share the schema
  `Registry,Assignment,Organization Name,Organization Address` (UTF-8).

## Gotchas

- **A locally administered address has no vendor, and that is an answer.**
  U/L bit set means a randomized MAC, a virtual NIC, or a container interface.
  Report it as *vendor lookup does not apply* — not as "vendor unknown". The two
  read the same to a careless caller and lead to opposite conclusions about
  whether a device is identifiable at all.
- **Never match on the first six digits.** MA-M (28-bit) and MA-S/IAB (36-bit)
  subdivide 24-bit prefixes that MA-L also uses, so one OUI can belong to many
  vendors. Match 36 → 28 → 24, longest first.
- **`Private` means assigned-but-undisclosed**, not unassigned. It is common in
  MA-M. Do not flatten it into the not-found path.
- **Do not spoof a browser User-Agent.** The IEEE origin answers
  `User-Agent: Mozilla/5.0` with `418 I'm a Teapot`, while a plain client UA
  gets `200` (measured 2026-07-26). `ieee.UserAgent` names the tool honestly —
  keep it that way.
- **Conditional GET is worth it.** Every registry file carries `ETag` and
  `Last-Modified`; a no-op refresh should cost a `304`, not 5.6 MB.
- **Registrant addresses are free text.** Inconsistent formatting means no
  country code can be parsed out reliably. Keep the field verbatim in `--json`
  and add no derived fields.
- **Exit-code contract depends on mode:** a single positional address in text
  mode is tri-state `0`/`1`/`2`; multiple addresses, stdin, or `--json` switch
  to batch mode (per-address results on stdout, error-only exit code). Don't
  "normalize" a single-address no-name result to `0`.
- **`search_vendor` is file-mediated.** Unlike `lookup_mac`, a vendor search can
  return hundreds of rows, so results are written under `workspace_root` and
  returned as a path.

## Data sources

All from the IEEE Registration Authority
(<https://standards.ieee.org/products-programs/regauth/>), public, no
authentication. Counts measured 2026-07-26.

| Registry | Path | Assignment width | Entries |
|---|---|---|---|
| MA-L | `/oui/oui.csv` | 24-bit | 39,812 |
| MA-M | `/oui28/mam.csv` | 28-bit | 6,501 |
| MA-S | `/oui36/oui36.csv` | 36-bit | 7,109 |
| IAB | `/iab/iab.csv` | 36-bit | 4,575 |
| CID | `/cid/cid.csv` | — | 215 |

The EtherType, Manufacturer ID and Operator ID files IEEE also publishes are
deliberately not consumed: they play no part in resolving a MAC address.

The cached data is local and not redistributed.
