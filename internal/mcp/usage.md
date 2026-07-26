# mac-lookup MCP server — operating manual

Resolves a MAC address or BSSID to its manufacturer **and to the kind of
address it is**, entirely offline from a locally cached copy of the IEEE
Registration Authority registries. No credentials are required. No lookup
touches the network, so investigating an address leaves no trace anywhere the
observed party can see.

## The one thing to get right

Read `vendor_lookup_applicable` **before** reading `vendor`.

When it is `false`, the address is broadcast, multicast, or locally
administered — a randomized MAC from a phone, a virtual NIC, a container
interface. There is no manufacturer, and no further lookup will produce one.
An empty `vendor` in that case does not mean the lookup missed; it means there
is nothing to miss. Treating a randomized MAC as an unidentified device is the
single most common way to reach a wrong conclusion from this data.

Every result also carries a `note` explaining, in words, why no vendor name is
present. Prefer it to inferring from an empty field.

**One exception worth knowing.** A locally administered address can still carry
a `vendor`, when it matches the CID registry — the registry that exists
precisely to assign locally administered prefixes. `vendor_lookup_applicable`
stays `false` there, because the OUI registries genuinely do not cover the
address. The name tells you which organization assigned the prefix; it does not
make the address a stable device identity, and it is still not safe to
correlate across networks. The `note` says so on every such result.

## Lifecycle

1. Call `db_status`. If it errors with "no local IEEE registry cache", call
   `update_db` once (about 5.6 MB, no credentials).
2. Call `lookup_mac` freely — it is a local, in-memory match.
3. `update_db` again only when `db_status` reports `stale: true`. IEEE
   regenerates the files roughly daily, and refreshes are conditional, so an
   unchanged file costs nothing.

## Tools

### `get_usage`

Returns this manual. No arguments.

### `lookup_mac`

| Argument | Type | Notes |
|---|---|---|
| `mac` | string | A single address, BSSID, or 24/28/36-bit prefix |
| `macs` | string[] | Several at once |

Accepted forms: `00:11:22:33:44:55`, `00-11-22-33-44-55`, `001122334455`,
`0011.2233.4455`, and bare prefixes (`00:11:22`, `8C:1F:64:AF:A`).

Result fields, per address:

| Field | Meaning |
|---|---|
| `input` | Your original text |
| `mac` | Canonical upper-case form |
| `bits` | 24, 28, 36 or 48 — how much address you supplied |
| `cast` | `unicast`, `multicast`, or `broadcast` |
| `administration` | `universal` (an IEEE assignment) or `local` (assigned locally) |
| `vendor_lookup_applicable` | Whether the OUI registries can say anything at all |
| `status` | `vendor`, `private`, `subdivided`, `not_found`, or `not_applicable` |
| `vendor` | The registrant name, present only for `status: vendor` |
| `match` | The assignment: `registry`, `assignment`, `prefix_bits`, `organization`, `address`, `private` |
| `well_known` | Name of a reserved range (STP, IPv6 multicast, VRRP, ...) when one applies |
| `note` | Why there is no vendor name |
| `error` | Set instead of everything else when the input could not be parsed |

Reading `status`:

- **`vendor`** — a named registrant. `match.prefix_bits` says how specific the
  assignment was (24, 28, or 36 bits).
- **`private`** — assigned, but the registrant withheld their name from the
  public registry. Nothing further will reveal it.
- **`subdivided`** — the 24-bit block is one IEEE retains and resells as
  smaller assignments, and none of them covers this address. If you supplied
  only a prefix, supply the full 48-bit address and try again. Never report
  "IEEE Registration Authority" as a manufacturer.
- **`not_found`** — a universally administered address with no assignment.
  Rare; usually a typo or a synthetic address.
- **`not_applicable`** — broadcast, multicast, or a locally administered
  address matching no CID. See "The one thing to get right" above.

### `search_vendor`

| Argument | Type | Notes |
|---|---|---|
| `query` | string | **Required.** Substring of the registrant name, case-insensitive |
| `workspace_root` | string | Writable directory for the results file; defaults to the configured workspace |
| `limit` | integer | Maximum rows to write; `0` means no limit |

**File-mediated.** A large vendor holds hundreds of assignments, so the rows
are written as JSON Lines and only the path is returned:

| Field | Meaning |
|---|---|
| `matches_file` | Path to read; one assignment per line |
| `total` | Matches found |
| `written` | Rows actually written |
| `truncated` | Present when `limit` cut the list short |

### `update_db`

Downloads MA-L, MA-M, MA-S, IAB and CID and rebuilds the store. No arguments.

`not_modified` lists registries the server confirmed unchanged (a 304).
`served_from_cache_after_failure` lists registries it could **not** reach, whose
cached entries were kept — the data is stale, not merely unchanged. Check that
field before trusting a fresh-looking `generated` timestamp.

### `db_status`

Reports `generated`, `assignments`, per-registry counts, `sources`, `stale`,
and `age_hours`. No arguments.

## Error recovery

| Message | Cause | Do this |
|---|---|---|
| `no local IEEE registry cache` | Nothing downloaded yet | Call `update_db` once |
| `invalid address: expected 12 hex digits ...` | Input is not a MAC or a valid prefix | Check for a truncated paste; prefixes must be 24, 28, or 36 bits |
| `provide 'mac' ... or 'macs' ...` | No address argument | Pass `mac` or `macs` |
| `provide 'query' ...` | Empty `search_vendor` query | Pass a registrant substring |
| `no workspace available` | `search_vendor` has nowhere to write | Pass `workspace_root` |
| `update failed: ... HTTP 418 ...` | Something rewrote the User-Agent to look like a browser | The IEEE origin rejects browser-like agents; the tool's own agent is correct — check for an intercepting proxy |
| `update failed: MA-L is required` | The base registry could not be fetched and nothing was cached | Check connectivity, then retry `update_db` |

## Data

IEEE Registration Authority public registries — MA-L (24-bit), MA-M (28-bit),
MA-S (36-bit), IAB (36-bit) and CID — about 58,000 assignments.
<https://standards.ieee.org/products-programs/regauth/>

Matching is longest-prefix-first (36 → 28 → 24), because IEEE subdivides 24-bit
blocks and several vendors can share one OUI. CID is consulted only for locally
administered addresses, which is what that registry exists for.

The cached data is local and is not redistributed.
