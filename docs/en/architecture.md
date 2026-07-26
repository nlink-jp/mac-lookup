# mac-lookup architecture

This document records *why* the pieces are shaped the way they are. What each
package does is in [AGENTS.md](../../AGENTS.md); the decision history is in
[the RFP](mac-lookup-rfp.md).

## The central decision: classification is the product

The obvious way to build this tool is a hash map from OUI to vendor name. That
tool would be wrong often enough to be dangerous, for two independent reasons.

**A locally administered address has no manufacturer at all.** Since iOS 14 and
Android 10, phones present a different randomized MAC to every network they
join. Virtual NICs, container bridges and VRRP virtual routers do the same. All
of them set the U/L bit. A vendor table has nothing to say about these
addresses — and the failure mode matters: "vendor unknown" reads as *I could
not find it*, which invites more searching, while the truth is *there is
nothing to find*. An investigator who does not know the difference will spend
hours correlating an address that was synthesized ten minutes ago and will be
discarded tomorrow.

So classification runs first and unconditionally: broadcast, then the I/G bit
(multicast, named from a small table of protocol ranges), then the U/L bit. The
registry is consulted only for universally administered addresses. The output
carries `vendor_lookup_applicable` as a first-class field precisely so callers
cannot silently conflate the two cases.

**One OUI can belong to several vendors.** IEEE sells 24-bit blocks (MA-L) but
also subdivides them into 28-bit (MA-M) and 36-bit (MA-S, and historically IAB)
assignments. A 36-bit assignment lives inside a 24-bit prefix that another
organization may also appear under. Matching on the first six hex digits
therefore returns a plausible, confidently-formatted, wrong vendor. Matching is
longest-prefix-first: 36, then 28, then 24.

`macaddr` holds the classification and knows nothing about the registries;
`ouidb` holds the registries and knows nothing about classification. Keeping
them apart means the part with the subtle semantics is pure, small, and testable
without any data files.

## Why an offline cache rather than an API

There is no MAC-vendor API worth depending on, but even if there were, the whole
dataset is 5.6 MB and about 58,000 rows. Downloading it once and matching in
memory is simpler than any client, needs no index, and — the reason that
actually decides it — means a lookup emits no traffic. In an incident, querying
a third party about an address you just observed tells that third party what you
are investigating. Every tool in this family (`asn-lookup`, `tor-exit-lookup`,
`icloud-relay-lookup`) makes the same trade for the same reason.

## Why no credentials

The IEEE files are public. That is not a minor convenience: a tool with no token
has nothing to configure, nothing to rotate, nothing to leak into a log, and
nothing that can expire in the middle of an investigation. The config schema has
no credential field at all, so one cannot be added by accident.

## Fetch behaviour, and the 418

Two properties of the IEEE origin were measured on 2026-07-26 and drive the
fetcher's design.

Every registry file returns `ETag` and `Last-Modified`, so a refresh that finds
nothing new costs a `304` rather than 5.6 MB. Since the files are regenerated
roughly daily, the TTL defaults to 24 hours with a 6-hour floor; refetching more
often cannot surface anything and only spends someone else's bandwidth.

More surprisingly, the origin rejects requests that *claim to be a browser*:
`User-Agent: Mozilla/5.0` is answered with `418 I'm a Teapot`, while a plain
client User-Agent is served normally. This inverts the usual instinct to
disguise a scraper. `ieee.UserAgent` names the tool and links to its repository,
which is both the honest thing to do and the thing that works.

## Determinism and freshness

Serialization takes an explicit timestamp and sorts entries, so only
`engine.Update` ever reads the clock and identical input produces byte-identical
output — a store you can diff. Writes go through a temp file and a rename, so an
interrupted update cannot leave a half-written cache that later parses as a
smaller registry.

Freshness is stored inside the record (`generated_at`) rather than inferred from
the file's mtime, so copying, syncing or restoring the cache does not silently
make a year-old snapshot look current.

## What is deliberately absent

- **Aggregated third-party vendor lists** (Wireshark's `manuf` and friends).
  They are better curated in places, but they carry licenses that would
  propagate into this repository. IEEE originals only.
- **Live scanning.** This tool never puts a frame on the wire. It resolves what
  you already captured.
- **Country extraction from registrant addresses.** The field is free text with
  no consistent structure; a parsed `country` would be wrong often enough to
  poison any aggregate built on it. The address is kept verbatim instead.
- **BSSID cluster analysis** (inferring a shared radio from adjacent BSSIDs).
  Genuinely useful, genuinely heuristic — deferred to v0.2 so the accuracy work
  does not delay a correct single-address resolver.
