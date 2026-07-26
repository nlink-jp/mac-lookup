# RFP: mac-lookup

> Generated: 2026-07-26
> Status: Draft

## 1. Problem Statement

A CLI and MCP server that, given a MAC address or BSSID observed in packet
captures, wireless surveys, DHCP/ARP logs, or EDR asset inventories, immediately
reports both the manufacturer and — more importantly — **the nature of the
address itself**. It matches offline against a locally cached copy of the IEEE
Registration Authority public registries, so investigative lookups leave no
trace on the target.

The critical behaviour is that a randomized MAC (iOS/Android) or a virtual NIC —
that is, a Locally Administered Address — is reported not as "vendor unknown"
but explicitly as **"an address for which vendor lookup is inherently
meaningless"**. Failing to draw that distinction leads directly to
misidentifying devices during wireless investigation and incident response.
The target user is the nlink-jp operator during IR and wireless work.

It joins the offline-matching lookup family — `asn-lookup` (IP → AS/country),
`tor-exit-lookup` (Tor exit detection), `icloud-relay-lookup` (Private Relay
detection) — by adding an L2 viewpoint alongside the existing network-layer ones.

## 2. Functional Specification

### Commands / API Surface

The CLI subcommand layout mirrors `tor-exit-lookup`.

| Command | Description |
|---|---|
| `mac-lookup lookup <MAC>...` | Forward lookup of a MAC / BSSID / prefix. Accepts multiple arguments and stdin batches |
| `mac-lookup search <query>` | Reverse lookup: vendor name (substring) → list of assigned prefixes |
| `mac-lookup update` | Re-fetch the registries (conditional GET) |
| `mac-lookup status` | Cache freshness, entry counts, per-registry breakdown |
| `mac-lookup mcp` | Run as an MCP server over stdio |

MCP tools (mirroring `asn-lookup` / `tor-exit-lookup`):

| Tool | Description |
|---|---|
| `lookup_mac` | Resolve a MAC / BSSID / prefix |
| `search_vendor` | Reverse vendor lookup. Result sets can be large, so this is **file-mediated**: it takes a `workspace_root` and returns a `matches_file` |
| `db_status` | Cache state |
| `update_db` | Re-fetch the registries |
| `get_usage` | Full tool reference plus error-recovery table (pinned `usage.md`) |

### Resolution Logic (the core of this tool)

The order is fixed. **Semantic classification comes before vendor lookup.**

1. **Normalization** — accepted notations:
   - colon-separated `00:11:22:33:44:55`
   - hyphen-separated `00-11-22-33-44-55`
   - bare `001122334455`
   - Cisco dotted `0011.2233.4455`
   - **prefix only** (`00:11:22` / `8C:1F:64:AF:A`) — accepted at 24, 28 or
     36 bit length
   - case-insensitive
2. **Broadcast** — `FF:FF:FF:FF:FF:FF`
3. **I/G bit (bit 0 of the first octet) = 1 → multicast** — well-known addresses
   are named from a built-in table:

   | Prefix | Name |
   |---|---|
   | `01:80:C2` | IEEE 802.1D STP / LLDP / PAUSE, etc. |
   | `01:00:5E` | IPv4 multicast (RFC 1112) |
   | `33:33` | IPv6 multicast (RFC 2464) |
   | `01:00:0C` | Cisco CDP / VTP / PVST+ |
   | `00:00:5E:00:01` | VRRP (IPv4, RFC 5798) |
   | `00:00:5E:00:02` | VRRP (IPv6, RFC 5798) |
   | `01:1B:19` / `01:80:C2:00:00:0E` | PTP (IEEE 1588) |

4. **U/L bit (bit 1 of the first octet) = 1 → Locally Administered** —
   randomized MAC, virtual NIC, container, etc. **No vendor lookup is
   attempted**; the result carries `vendor_lookup_applicable: false`
5. **Longest-prefix match, universally administered addresses only** — try
   36 bit (MA-S / IAB), then 28 bit (MA-M), then 24 bit (MA-L). Multiple vendors
   share a single 24-bit prefix in split allocations, so matching on the first
   six digits alone produces wrong answers
6. **Distinguish `Private` registrations** — IEEE permits registrants to
   withhold their name. An organization name of `Private` means "assigned but
   name withheld", which is not the same as "unassigned"

### Input / Output

**text output** (default): one result per line, vendor name only, no address.

**`--json` output**: one object per input. Draft schema:

```json
{
  "input": "8c:1f:64:af:a0:01",
  "mac": "8C:1F:64:AF:A0:01",
  "cast": "unicast",
  "administration": "universal",
  "vendor_lookup_applicable": true,
  "match": {
    "registry": "MA-S",
    "assignment": "8C1F64AFA",
    "prefix_bits": 36,
    "organization": "DATA ELECTRONIC DEVICES, INC",
    "address": "32 NORTHWESTERN DR SALEM NH US 03079",
    "private": false
  },
  "well_known": null
}
```

- `cast`: `unicast` | `multicast` | `broadcast`
- `administration`: `universal` | `local`
- When `administration` is `local`, `match` is `null`,
  `vendor_lookup_applicable` is `false`, and `note` records that the address is
  likely a randomized MAC
- `well_known`: present only for multicast / special addresses, as
  `{name, prefix}`
- `address` preserves the CSV text verbatim. **No country code is extracted** —
  the field is free text with inconsistent formatting, so the risk of
  mis-extraction outweighs the benefit

**Exit codes (grep-style tri-state, as in `tor-exit-lookup`)**:

| Code | Meaning |
|---|---|
| 0 | Vendor identified |
| 1 | No name available (unassigned / LAA / multicast / `Private`) |
| 2 | Error |

The tri-state applies **only to a single MAC with text output**. Multiple
inputs, stdin, and `--json` are treated as batches and report error presence
only (0 / 2).

### Configuration

- Config file: sectioned TOML. The path is decided per tool (align it with the
  actual `tor-exit-lookup` layout during scaffolding)
- Settings: data directory, TTL, `auto_update` — nothing else
- **There are no credential settings**
- Environment variables prefixed `MAC_LOOKUP_` override the config file

### External Dependencies

Only the five public IEEE Registration Authority CSV files. No authentication.

| Registry | Path | Entries (measured 2026-07-26) | Size |
|---|---|---|---|
| MA-L (24 bit) | `oui/oui.csv` | 39,812 | 3.8 MB |
| MA-M (28 bit) | `oui28/mam.csv` | 6,501 | 740 KB |
| MA-S (36 bit) | `oui36/oui36.csv` | 7,109 | 659 KB |
| IAB (36 bit) | `iab/iab.csv` | 4,575 | 381 KB |
| CID | `cid/cid.csv` | 215 | 20 KB |

- Host: `standards-oui.ieee.org`
- All five share **one 4-column schema**
  (`Registry,Assignment,Organization Name,Organization Address`, UTF-8), so a
  single parser suffices
- Roughly 58,000 entries / 5.6 MB in total — small enough to match against an
  in-memory map with no index
- **The data itself is never vendored into the repository** (fetched at runtime)

Zero external Go dependencies (`net/http` + `encoding/csv` + stdlib only).

## 3. Design Decisions

**Why Go** — the same language and skeleton as the existing lookup family
(`asn-lookup` / `tor-exit-lookup` / `icloud-relay-lookup`), which lets the
single-binary distribution and notarization practices carry over unchanged.
The data volume is also small enough to handle with zero dependencies.

**Skeleton sources** — primarily `tor-exit-lookup` (offline cache plus
`update`/`status`, zero-dependency JSON-RPC MCP, tri-state exit codes),
secondarily `asn-lookup` (TTL handling and `db_status` conventions).

**Complementary existing tools**:

- `asn-lookup` — L3 attribution (IP → AS/country); this tool provides L2
  attribution (MAC → manufacturer)
- `tor-exit-lookup` / `icloud-relay-lookup` / `abuse-lookup` — the multi-angle
  IP profiling set; this adds a wireless/LAN-side observation point
- `ai-ir2` / `ir-hub` — consume it over MCP from the IR analysis pipeline

**Explicitly out of scope**:

- Ingesting secondary aggregated lists such as Wireshark's `manuf` (avoids GPL
  contamination; IEEE originals only)
- Live ARP / wireless scanning, packet capture
- BSSID cluster analysis (inferring a shared AP/radio from adjacent BSSIDs) is
  deferred to **v0.2 or later**; v0.1 concentrates on resolving a single MAC
  correctly
- GUI
- The IEEE EtherType / Manufacturer ID / Operator ID files (not needed for MAC
  resolution)

## 4. Development Plan

### Phase 1: Core

- Registry parser (one parser for the shared 5-CSV schema)
- Deterministic store (fixed ordering so diffs stay stable)
- Longest-prefix resolver (36 → 28 → 24 bit)
- MAC normalization plus semantic classification (broadcast / multicast with the
  well-known table / LAA / universal)
- IEEE downloader (conditional GET, TTL floor, honest User-Agent)
- Config loader
- HTTP behind an interface so it can be mocked

All logic must be unit-testable at this point.

### Phase 2: Features

- CLI: `lookup` / `search` / `update` / `status`, tri-state exit codes,
  `--json`, stdin batches
- MCP server (zero-dependency JSON-RPC): `lookup_mac` / `search_vendor` /
  `db_status` / `update_db` / `get_usage`
- File-mediated output for `search_vendor` (`workspace_root` → `matches_file`)

### Phase 3: Release

- README.md / README.ja.md / AGENTS.md / CHANGELOG.md / pinned `usage.md`
- Real-data E2E plus a live simulation using the built binary
- `make build-all` → 4 platforms; darwin arm64 notarized and verified with
  `spctl`
- GitHub release (canonical binary name inside each zip)
- homebrew-tap formula (sha256 verified against the published asset)
- Add as a submodule of `cybersecurity-series`
- Update both surfaces: org profile (alphabetical) and the web catalog (EN/JA)
- `check-org.sh` all green

All three phases can be reviewed independently.

## 5. Required API Scopes / Permissions

**None.** The IEEE CSV files are public and unauthenticated. No API key, OAuth
scope, or IAM role is required, and the config file holds no credential fields.

## 6. Series Placement

Series: **cybersecurity-series**

Reason: MAC/BSSID analysis is used in incident response and wireless
investigation. The data itself is neutral asset information, which made
`util-series` a candidate, but following the precedent set by `tor-exit-lookup`
— place by purpose rather than by matching mechanism — it sits alongside the
existing `*-lookup` family (`whois` / `abuse` / `icloud-relay` / `tor-exit` /
`doh` / `urlscan`), which favours discoverability.

## 7. External Platform Constraints

- **A spoofed browser User-Agent is rejected with 418** — sending
  `User-Agent: Mozilla/5.0` returns `418 I'm a Teapot`, while a plain
  `curl/8.x` UA returns `200` (measured 2026-07-26). Do not spoof a browser;
  send an honest User-Agent naming the tool
- **Conditional GET works** — all five files return `ETag` and `Last-Modified`,
  so `If-None-Match` yields `304` and avoids a pointless 5.6 MB transfer
- **Regenerated daily** — as of 2026-07-26 every file's `Last-Modified` is
  around 00:01 UTC that same day, so assume roughly one update per day
- **Courtesy toward IEEE** — as with `tor-exit-lookup`, impose a TTL floor to
  prevent rapid repeated fetches
- **Licensing** — the IEEE page states no explicit terms of use. Fetching at
  runtime instead of vendoring the data sidesteps the redistribution question
- **`Private` registrations exist** — registrants may withhold their name, so
  "assigned but unnameable" is a structural case (common in MA-M)

---

## Discussion Log

**2026-07-26 — proposal and direction setting**

- Origin: the suggestion that "the lookup family could use one more member for
  OUI codes — it would help with MAC address and BSS analysis, and downloading
  and caching the IEEE data would match how the existing tools work"
- The data source was measured directly: five files, ~58,000 entries, 5.6 MB,
  one shared schema, `ETag`/`Last-Modified` present, no authentication required
- During measurement, the **418 rejection of spoofed browser User-Agents**
  (plain UA returns 200) was discovered and recorded as a constraint

**Key design observations**

1. A naive "first six digits" lookup produces wrong answers: MA-L, MA-M, and
   MA-S/IAB share 24-bit prefixes in split allocations, so a 36 → 28 → 24
   longest-prefix match is mandatory
2. What actually matters in BSSID/MAC analysis comes before the vendor name:
   **the semantics of the address**. Reporting an LAA (randomized MAC) as
   "vendor unknown" leads straight to misidentifying a device, so reporting it
   as "lookup is meaningless here" is placed at the core of the design

**Decisions (all approved by the user)**

| Question | Decision | Rejected alternative |
|---|---|---|
| Tool name | `mac-lookup` | `oui-lookup` (leaves the core LAA/multicast classification out of the name) |
| Series placement | cybersecurity-series | util-series (neutral asset data, but purpose and discoverability won) |
| BSSID cluster analysis | v0.2 or later | include in v0.1 (heuristic accuracy validation cost) |
| Exit codes | 0/1/2 tri-state | binary 0/2 |
| Reverse vendor lookup | include in v0.1 | defer to v0.2 |
| Registration address | keep verbatim in `--json` | extract country code (inconsistent formatting, mis-extraction risk) / drop entirely |
