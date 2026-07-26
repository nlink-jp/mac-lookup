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
package ouidb

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

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

const (
	// PrivateOrganization is the literal IEEE writes when a registrant chose to
	// withhold their name. It means "assigned, name not disclosed", which is a
	// different answer from "unassigned" and must not be flattened into it.
	PrivateOrganization = "Private"
	// RegistryHeldOrganization is the literal IEEE writes for a 24-bit block it
	// retains in order to sell it off as smaller MA-M / MA-S / IAB assignments.
	// Around 429 MA-L rows look like this. Such a row is emphatically not a
	// manufacturer: reporting "vendor: IEEE Registration Authority" for a NIC
	// would be nonsense. It means the OUI is subdivided and the specific
	// sub-assignment was not found.
	RegistryHeldOrganization = "IEEE Registration Authority"
)

// Entry is one registry assignment.
type Entry struct {
	Registry Registry `json:"registry"`
	// Assignment is the hex prefix as IEEE writes it, upper-case, no separators
	// (for example "8C1F64AFA" for a 36-bit MA-S assignment).
	Assignment string `json:"assignment"`
	// PrefixBits is Registry.Bits(), stored so consumers need no lookup table.
	PrefixBits int `json:"prefix_bits"`
	// Organization is the registrant name, or one of the two sentinel literals
	// above.
	Organization string `json:"organization"`
	// Address is the registrant address, kept verbatim from the CSV. IEEE writes
	// it as free text with inconsistent formatting, so no country code or other
	// field is extracted from it.
	Address string `json:"address"`
}

// Private reports whether the registrant withheld their name.
func (e Entry) Private() bool { return e.Organization == PrivateOrganization }

// RegistryHeld reports whether this row is a block IEEE retained for
// subdivision rather than an assignment to a manufacturer.
func (e Entry) RegistryHeld() bool { return e.Organization == RegistryHeldOrganization }

// MatchStatus explains what a lookup found. The distinctions exist because
// "no name" arises for four different reasons that call for different actions.
type MatchStatus string

const (
	// MatchVendor: a named registrant was found.
	MatchVendor MatchStatus = "vendor"
	// MatchPrivate: assigned, but the registrant withheld their name. No further
	// lookup will help.
	MatchPrivate MatchStatus = "private"
	// MatchSubdivided: the covering 24-bit block is held by IEEE for subdivision.
	// Supplying more of the address (up to 36 bits) may resolve it; if the full
	// address was already supplied, the sub-block is simply unassigned.
	MatchSubdivided MatchStatus = "subdivided"
	// MatchNotFound: no assignment covers this address.
	MatchNotFound MatchStatus = "not_found"
)

// DB is the queryable registry database: roughly 58,000 assignments, small
// enough to hold in memory with no index beyond a map per lookup width.
type DB struct {
	// GeneratedAt is when the store was built, as a Unix timestamp. Freshness
	// lives in the record rather than the file mtime so it survives copies.
	GeneratedAt int64 `json:"generated_at"`
	// Sources records the URL and validator (ETag / Last-Modified) each registry
	// was fetched from, so `status` can explain what is cached.
	Sources []Source `json:"sources"`
	// Entries is sorted by assignment for deterministic serialization.
	Entries []Entry `json:"entries"`

	// oui indexes the universally administered registries (MA-L/MA-M/MA-S/IAB)
	// by assignment; cid indexes the CID registry, which only ever applies to
	// locally administered addresses.
	oui map[string]Entry
	cid map[string]Entry
}

// Source records the provenance of one registry file.
type Source struct {
	Registry     Registry `json:"registry"`
	URL          string   `json:"url"`
	ETag         string   `json:"etag,omitempty"`
	LastModified string   `json:"last_modified,omitempty"`
	Count        int      `json:"count"`
}

// ErrMalformed means the CSV did not have the shape every IEEE registry file
// shares.
var ErrMalformed = errors.New("malformed registry CSV")

// ParseCSV reads one registry file. fallback names the registry for rows whose
// first column is blank. It returns the parsed entries and the number of rows
// skipped as unusable, so an upstream format change shows up as a count rather
// than as silently missing vendors.
func ParseCSV(r io.Reader, fallback Registry) ([]Entry, int, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate trailing-column drift
	cr.ReuseRecord = true

	header, err := cr.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if len(header) < 3 || !strings.EqualFold(strings.TrimSpace(header[0]), "Registry") {
		return nil, 0, fmt.Errorf("%w: unexpected header %q", ErrMalformed, strings.Join(header, ","))
	}

	var entries []Entry
	skipped := 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		if len(rec) < 3 {
			skipped++
			continue
		}
		assignment := strings.ToUpper(strings.TrimSpace(rec[1]))
		if assignment == "" || !isHex(assignment) {
			skipped++
			continue
		}
		reg := Registry(strings.TrimSpace(rec[0]))
		if reg == "" {
			reg = fallback
		}
		address := ""
		if len(rec) > 3 {
			address = strings.TrimSpace(rec[3])
		}
		entries = append(entries, Entry{
			Registry:     reg,
			Assignment:   assignment,
			PrefixBits:   reg.Bits(),
			Organization: strings.TrimSpace(rec[2]),
			Address:      address,
		})
	}
	return entries, skipped, nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// New builds a DB from parsed entries. generatedUnix is passed in rather than
// read from the clock so serialization stays deterministic and testable.
func New(entries []Entry, sources []Source, generatedUnix int64) *DB {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Assignment != sorted[j].Assignment {
			return sorted[i].Assignment < sorted[j].Assignment
		}
		return sorted[i].Registry < sorted[j].Registry
	})
	db := &DB{GeneratedAt: generatedUnix, Sources: sources, Entries: sorted}
	db.buildIndex()
	return db
}

func (d *DB) buildIndex() {
	d.oui = make(map[string]Entry, len(d.Entries))
	d.cid = make(map[string]Entry)
	for _, e := range d.Entries {
		if e.Registry == RegistryCID {
			d.cid[e.Assignment] = e
			continue
		}
		d.oui[e.Assignment] = e
	}
}

// Serialize writes the database as JSON. Entries are already sorted, so
// identical input yields byte-identical output.
func Serialize(w io.Writer, db *DB) error {
	return json.NewEncoder(w).Encode(db)
}

// Open parses a serialized store and rebuilds its indexes.
func Open(data []byte) (*DB, error) {
	var db DB
	if err := json.Unmarshal(data, &db); err != nil {
		return nil, fmt.Errorf("parse store: %w", err)
	}
	db.buildIndex()
	return &db, nil
}

// Generated returns when the store was built.
func (d *DB) Generated() time.Time { return time.Unix(d.GeneratedAt, 0).UTC() }

// Len returns the number of assignments held.
func (d *DB) Len() int { return len(d.Entries) }

// CountsByRegistry reports how many assignments came from each registry.
func (d *DB) CountsByRegistry() map[Registry]int {
	out := make(map[Registry]int, 5)
	for _, e := range d.Entries {
		out[e.Registry]++
	}
	return out
}

// lookupWidths is the order universally administered addresses are matched in:
// longest assignment first. Matching 24 bits first would return the block
// holder — often IEEE itself — instead of the vendor who owns the 36-bit
// sub-assignment.
var lookupWidths = []int{9, 7, 6} // hex digits: 36, 28, 24 bits

// Lookup resolves a universally administered address or prefix. hex must be
// upper-case with no separators. The status distinguishes a real vendor from a
// withheld name, a subdivided block, and a genuine miss.
func (d *DB) Lookup(hex string) (Entry, MatchStatus) {
	for _, w := range lookupWidths {
		if len(hex) < w {
			continue
		}
		e, ok := d.oui[hex[:w]]
		if !ok {
			continue
		}
		switch {
		case e.RegistryHeld():
			// Only a 24-bit row is ever registry-held, and reaching it means the
			// narrower assignment was not found. Keep searching no further: the
			// wider block is the most specific thing that exists.
			return e, MatchSubdivided
		case e.Private():
			return e, MatchPrivate
		default:
			return e, MatchVendor
		}
	}
	return Entry{}, MatchNotFound
}

// LookupCID resolves a locally administered address against the CID registry.
// CID assignments exist precisely to build locally administered addresses — the
// U/L bit is set by construction — so this is the one registry that can name
// the assigner of an address the OUI registries cannot touch. Most locally
// administered addresses are randomized and match nothing, which is itself the
// expected answer.
func (d *DB) LookupCID(hex string) (Entry, MatchStatus) {
	if len(hex) < 6 {
		return Entry{}, MatchNotFound
	}
	e, ok := d.cid[hex[:6]]
	if !ok {
		return Entry{}, MatchNotFound
	}
	if e.Private() {
		return e, MatchPrivate
	}
	return e, MatchVendor
}

// Search returns assignments whose organization contains query, compared
// case-insensitively. Results keep the deterministic entry order. A limit of 0
// or less means no limit; the second return value is the total number of
// matches before truncation, so a caller can report what it did not show.
func (d *DB) Search(query string, limit int) ([]Entry, int) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, 0
	}
	var out []Entry
	total := 0
	for _, e := range d.Entries {
		if !strings.Contains(strings.ToLower(e.Organization), q) {
			continue
		}
		total++
		if limit > 0 && len(out) >= limit {
			continue
		}
		out = append(out, e)
	}
	return out, total
}
