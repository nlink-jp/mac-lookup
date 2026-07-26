// Package ouidb parses the IEEE Registration Authority registry files, holds
// them as a longest-prefix-matchable database, and serializes that database to
// a local store. It is pure: no network access, no clock reads.
//
// All five registry files share one CSV schema
// (Registry,Assignment,Organization Name,Organization Address), so a single
// parser covers them. What differs is the assignment width, and that width is
// the reason matching cannot be a simple first-six-digits lookup: MA-M and
// MA-S/IAB carve up 24-bit prefixes that MA-L also uses, so several vendors
// legitimately share one OUI. Matching must try 36 bits, then 28, then 24.
//
// Scaffold: types are fixed here; parsing, matching and the store are
// implemented in Phase 3.
package ouidb

// Registry identifies which IEEE registry an assignment came from. The value is
// the string IEEE itself puts in the CSV's first column.
type Registry string

const (
	RegistryMAL Registry = "MA-L" // 24-bit assignment (the classic OUI)
	RegistryMAM Registry = "MA-M" // 28-bit assignment
	RegistryMAS Registry = "MA-S" // 36-bit assignment
	RegistryIAB Registry = "IAB"  // 36-bit assignment (legacy, superseded by MA-S)
	RegistryCID Registry = "CID"  // Company ID: not an OUI, for locally administered use
)

// Bits reports the assignment width of a registry in bits.
func (r Registry) Bits() int {
	switch r {
	case RegistryMAM:
		return 28
	case RegistryMAS, RegistryIAB:
		return 36
	default:
		return 24
	}
}

// PrivateOrganization is the literal IEEE writes when a registrant chose to
// withhold their name. It means "assigned, name not disclosed", which is a
// different answer from "unassigned" and must not be flattened into it.
const PrivateOrganization = "Private"

// Entry is one registry assignment.
type Entry struct {
	Registry Registry `json:"registry"`
	// Assignment is the hex prefix as IEEE writes it, upper-case, no separators
	// (for example "8C1F64AFA" for a 36-bit MA-S assignment).
	Assignment string `json:"assignment"`
	// PrefixBits is Registry.Bits(), stored so consumers need no lookup table.
	PrefixBits int `json:"prefix_bits"`
	// Organization is the registrant name, or PrivateOrganization.
	Organization string `json:"organization"`
	// Address is the registrant address, kept verbatim from the CSV. IEEE writes
	// it as free text with inconsistent formatting, so no country code or other
	// field is extracted from it.
	Address string `json:"address"`
}

// Private reports whether the registrant withheld their name.
func (e Entry) Private() bool { return e.Organization == PrivateOrganization }

// DB is the queryable registry database: roughly 58,000 assignments, small
// enough to hold in memory with no index.
type DB struct {
	// GeneratedAt is when the store was built, as a Unix timestamp. Freshness
	// lives in the record rather than the file mtime so it survives copies.
	GeneratedAt int64 `json:"generated_at"`
	// Sources records the URL and validator (ETag / Last-Modified) each registry
	// was fetched from, so `status` can explain what is cached.
	Sources []Source `json:"sources"`
	// Entries is sorted by assignment for deterministic serialization.
	Entries []Entry `json:"entries"`
}

// Source records the provenance of one registry file.
type Source struct {
	Registry     Registry `json:"registry"`
	URL          string   `json:"url"`
	ETag         string   `json:"etag,omitempty"`
	LastModified string   `json:"last_modified,omitempty"`
	Count        int      `json:"count"`
}
