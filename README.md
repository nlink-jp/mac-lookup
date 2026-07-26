# mac-lookup

Resolve a MAC address or BSSID to its manufacturer — and, before that, to
**what kind of address it actually is**. Offline, from a locally cached copy of
the IEEE Registration Authority public registries. Runs as a CLI and as a local
MCP server.

The L2 sibling of [`asn-lookup`](https://github.com/nlink-jp/asn-lookup)
(IP → AS/country) and [`tor-exit-lookup`](https://github.com/nlink-jp/tor-exit-lookup)
(offline membership).

> **Status: scaffold.** The build, packaging and command surface are in place;
> the resolver lands in Phase 3. See
> [docs/en/mac-lookup-rfp.md](docs/en/mac-lookup-rfp.md).

## Why

A vendor table alone gives wrong answers. Two things have to happen first:

- **A randomized MAC has no manufacturer.** Modern phones rotate a locally
  administered address per network, and virtual NICs and containers do the same
  thing. Reporting that as "vendor unknown" invites an investigator to keep
  hunting for a device that was never registered. `mac-lookup` reports it as
  *locally administered — vendor lookup does not apply*.
- **The first six digits are not enough.** IEEE splits 24-bit prefixes into
  28-bit (MA-M) and 36-bit (MA-S/IAB) assignments, so several vendors share one
  OUI. `mac-lookup` matches longest-prefix-first (36 → 28 → 24).

Everything is answered from a local cache, so looking up an address never
touches a network the target can observe.

## Install

```bash
brew install nlink-jp/tap/mac-lookup
```

Or build from source (Go 1.25+, no external dependencies):

```bash
make build
```

The binary lands in `dist/mac-lookup`.

## Usage

```bash
mac-lookup update                        # download the IEEE registries once
mac-lookup lookup 8C:1F:64:AF:A0:01      # resolve an address
mac-lookup lookup --json < captured.txt  # batch from stdin, JSON Lines out
mac-lookup search Apple                  # vendor name → assigned prefixes
mac-lookup status                        # cache freshness and size
mac-lookup mcp                           # run as a local MCP server (stdio)
```

Accepted address forms: `00:11:22:33:44:55`, `00-11-22-33-44-55`,
`001122334455`, Cisco's `0011.2233.4455`, and bare prefixes at 24, 28 or 36
bits (`00:11:22`, `8C:1F:64:AF:A`).

### Exit codes

`lookup` uses grep-style exit codes for a **single address in text mode**:

| Code | Meaning |
|---|---|
| `0` | resolved to a named vendor |
| `1` | no vendor name — unassigned, locally administered, multicast, or a `Private` registration |
| `2` | error |

```bash
if mac-lookup lookup "$mac"; then echo "vendor known"; fi
```

Multiple addresses, stdin, or `--json` switch to batch mode: results go to
stdout and the exit code reports errors only (`0`/`2`).

## Configuration

Optional. Copy [`config.example.toml`](config.example.toml) to
`~/.config/mac-lookup/config.toml`. Every value also has a `MAC_LOOKUP_*`
environment override.

**There are no credentials.** The IEEE registry files are public, so there is
no token or API key to configure, log, or leak.

The cache auto-refetches when older than the TTL (default 24h, floored at 6h —
IEEE regenerates the files about once a day). Disable with `--no-update` or
`[ieee] auto_update = false`.

## MCP server

`mac-lookup mcp` speaks JSON-RPC 2.0 over stdio and exposes `lookup_mac`,
`search_vendor`, `db_status`, `update_db` and `get_usage`. Call `get_usage`
first — it returns the full tool reference and error-recovery table.

`search_vendor` is file-mediated: a popular vendor holds hundreds of prefixes,
so the result is written under the caller's `workspace_root` and returned as a
`matches_file` path.

## Data

IEEE Registration Authority public registries — MA-L, MA-M, MA-S, IAB and CID
(<https://standards.ieee.org/products-programs/regauth/>). About 58,000
assignments in total. No authentication is required.

The registry data is downloaded at runtime and cached locally; it is not
redistributed with this tool.

## License

MIT. See [LICENSE](LICENSE).
