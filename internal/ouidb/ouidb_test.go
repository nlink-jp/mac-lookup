package ouidb

import (
	"bytes"
	"strings"
	"testing"
)

func TestRegistryBits(t *testing.T) {
	tests := []struct {
		registry Registry
		want     int
	}{
		{RegistryMAL, 24},
		{RegistryMAM, 28},
		{RegistryMAS, 36},
		{RegistryIAB, 36},
		{RegistryCID, 24},
	}
	for _, tt := range tests {
		if got := tt.registry.Bits(); got != tt.want {
			t.Errorf("Registry(%q).Bits() = %d, want %d", tt.registry, got, tt.want)
		}
	}
}

func TestEntrySentinels(t *testing.T) {
	// IEEE lets a registrant withhold their name. "Private" means assigned but
	// undisclosed, which callers must not flatten into "unassigned".
	if !(Entry{Organization: PrivateOrganization}).Private() {
		t.Error(`Entry{Organization: "Private"}.Private() = false, want true`)
	}
	// A block IEEE retains for subdivision is not a manufacturer.
	if !(Entry{Organization: RegistryHeldOrganization}).RegistryHeld() {
		t.Error("registry-held row not recognised")
	}
	named := Entry{Organization: "Nokia Shanghai Bell Co., Ltd."}
	if named.Private() || named.RegistryHeld() {
		t.Error("named registrant misclassified as a sentinel")
	}
	if (Entry{}).Private() || (Entry{}).RegistryHeld() {
		t.Error("empty organization misclassified as a sentinel")
	}
}

// The header and quoting here match the real IEEE files, including a trailing
// space inside the address field.
const sampleMAL = `Registry,Assignment,Organization Name,Organization Address
MA-L,286FB9,"Nokia Shanghai Bell Co., Ltd.","No.388 Ning Qiao Road Shanghai CN 201206 "
MA-L,8C1F64,IEEE Registration Authority,445 Hoes Lane Piscataway NJ US 08554
MA-L,741AE0,IEEE Registration Authority,445 Hoes Lane Piscataway NJ US 08554
MA-L,FFFFFF,Bogus Row Without Hex Assignment,
`

const sampleMAS = `Registry,Assignment,Organization Name,Organization Address
MA-S,8C1F64AFA,"DATA ELECTRONIC DEVICES, INC",32 NORTHWESTERN DR SALEM NH US 03079
MA-S,8C1F649B9,"QUERCUS TECHNOLOGIES, S.L.",Av. Onze de Setembre 19 Reus ES 43203
`

const sampleMAM = `Registry,Assignment,Organization Name,Organization Address
MA-M,741AE09,Private,
`

const sampleCID = `Registry,Assignment,Organization Name,Organization Address
CID,EA2701,ACCE Technology Corp.,"6F.-2, No. 38, Beida Rd., Hsinchu City TW 300024 "
`

func testDB(t *testing.T) *DB {
	t.Helper()
	var all []Entry
	for _, s := range []struct {
		csv string
		reg Registry
	}{{sampleMAL, RegistryMAL}, {sampleMAS, RegistryMAS}, {sampleMAM, RegistryMAM}, {sampleCID, RegistryCID}} {
		entries, _, err := ParseCSV(strings.NewReader(s.csv), s.reg)
		if err != nil {
			t.Fatalf("ParseCSV(%s) returned error: %v", s.reg, err)
		}
		all = append(all, entries...)
	}
	return New(all, nil, 1753488000)
}

func TestParseCSV(t *testing.T) {
	entries, skipped, err := ParseCSV(strings.NewReader(sampleMAL), RegistryMAL)
	if err != nil {
		t.Fatalf("ParseCSV returned error: %v", err)
	}
	if len(entries) != 4 || skipped != 0 {
		t.Fatalf("got %d entries / %d skipped, want 4 / 0", len(entries), skipped)
	}
	got := entries[0]
	if got.Organization != "Nokia Shanghai Bell Co., Ltd." {
		t.Errorf("Organization = %q", got.Organization)
	}
	if got.PrefixBits != 24 {
		t.Errorf("PrefixBits = %d, want 24", got.PrefixBits)
	}
	// The registrant address is free text and is kept exactly as IEEE wrote it,
	// minus surrounding whitespace. Nothing is parsed out of it.
	if got.Address != "No.388 Ning Qiao Road Shanghai CN 201206" {
		t.Errorf("Address = %q", got.Address)
	}
}

func TestParseCSVRejectsForeignFormat(t *testing.T) {
	if _, _, err := ParseCSV(strings.NewReader("a,b,c\n1,2,3\n"), RegistryMAL); err == nil {
		t.Error("ParseCSV accepted a CSV that is not an IEEE registry file")
	}
	if _, _, err := ParseCSV(strings.NewReader(""), RegistryMAL); err == nil {
		t.Error("ParseCSV accepted an empty file")
	}
}

func TestParseCSVSkipsUnusableRows(t *testing.T) {
	// A row whose assignment is not hex cannot be matched against; it is counted
	// rather than dropped silently, so an upstream format change is visible.
	in := "Registry,Assignment,Organization Name,Organization Address\nMA-L,NOTHEX,Some Corp,\nMA-L,,Blank Corp,\n"
	entries, skipped, err := ParseCSV(strings.NewReader(in), RegistryMAL)
	if err != nil {
		t.Fatalf("ParseCSV returned error: %v", err)
	}
	if len(entries) != 0 || skipped != 2 {
		t.Errorf("got %d entries / %d skipped, want 0 / 2", len(entries), skipped)
	}
}

func TestLookupLongestPrefixWins(t *testing.T) {
	db := testDB(t)
	// 8C:1F:64 is subdivided: the 24-bit row belongs to IEEE, and the real
	// vendor holds the 36-bit assignment. Matching six digits first would name
	// the wrong organization.
	e, st := db.Lookup("8C1F64AFA001")
	if st != MatchVendor {
		t.Fatalf("status = %q, want %q", st, MatchVendor)
	}
	if e.Organization != "DATA ELECTRONIC DEVICES, INC" {
		t.Errorf("Organization = %q, want the 36-bit assignee", e.Organization)
	}
	if e.Registry != RegistryMAS || e.PrefixBits != 36 {
		t.Errorf("matched %s/%d bits, want MA-S/36", e.Registry, e.PrefixBits)
	}
}

func TestLookupSubdividedBlock(t *testing.T) {
	db := testDB(t)
	// Same OUI, but a sub-block nobody holds. The answer is "this OUI is
	// subdivided", not "vendor: IEEE Registration Authority".
	e, st := db.Lookup("8C1F64000001")
	if st != MatchSubdivided {
		t.Fatalf("status = %q, want %q", st, MatchSubdivided)
	}
	if !e.RegistryHeld() {
		t.Errorf("Organization = %q, want the registry-held sentinel", e.Organization)
	}

	// A bare 24-bit prefix of a subdivided block reaches the same conclusion.
	if _, st := db.Lookup("8C1F64"); st != MatchSubdivided {
		t.Errorf("24-bit prefix status = %q, want %q", st, MatchSubdivided)
	}
}

func TestLookupPrivateIsNotNotFound(t *testing.T) {
	db := testDB(t)
	e, st := db.Lookup("741AE09000001"[:12])
	if st != MatchPrivate {
		t.Fatalf("status = %q, want %q", st, MatchPrivate)
	}
	if e.Registry != RegistryMAM {
		t.Errorf("Registry = %q, want MA-M", e.Registry)
	}
}

func TestLookupNotFound(t *testing.T) {
	db := testDB(t)
	if _, st := db.Lookup("AABBCCDDEEFF"); st != MatchNotFound {
		t.Errorf("status = %q, want %q", st, MatchNotFound)
	}
	// A 24-bit prefix cannot match a 36-bit assignment: there is not enough
	// address to be sure which sub-block it falls in.
	if _, st := db.Lookup("286FB9"); st != MatchVendor {
		t.Errorf("24-bit MA-L prefix status = %q, want %q", st, MatchVendor)
	}
}

func TestLookupCIDIsSeparate(t *testing.T) {
	db := testDB(t)
	// CID assignments have the U/L bit set by construction (0xEA = ...1010), so
	// they are only ever reached through the locally administered path. They
	// must not pollute the OUI index.
	if _, st := db.Lookup("EA2701000001"); st != MatchNotFound {
		t.Errorf("CID prefix found in the OUI index (status %q)", st)
	}
	e, st := db.LookupCID("EA2701000001")
	if st != MatchVendor {
		t.Fatalf("LookupCID status = %q, want %q", st, MatchVendor)
	}
	if e.Organization != "ACCE Technology Corp." {
		t.Errorf("Organization = %q", e.Organization)
	}
	// A randomized MAC matches nothing, which is the expected answer.
	if _, st := db.LookupCID("DAA119000001"); st != MatchNotFound {
		t.Errorf("randomized address status = %q, want %q", st, MatchNotFound)
	}
}

func TestSerializeIsDeterministic(t *testing.T) {
	db := testDB(t)
	var a, b bytes.Buffer
	if err := Serialize(&a, db); err != nil {
		t.Fatalf("Serialize returned error: %v", err)
	}
	// Rebuilding from the same entries in a different order must produce the
	// same bytes, so the store can be diffed.
	shuffled := make([]Entry, len(db.Entries))
	for i, e := range db.Entries {
		shuffled[len(db.Entries)-1-i] = e
	}
	if err := Serialize(&b, New(shuffled, nil, db.GeneratedAt)); err != nil {
		t.Fatalf("Serialize returned error: %v", err)
	}
	if a.String() != b.String() {
		t.Error("serialization is not deterministic across input order")
	}
}

func TestRoundTrip(t *testing.T) {
	db := testDB(t)
	var buf bytes.Buffer
	if err := Serialize(&buf, db); err != nil {
		t.Fatalf("Serialize returned error: %v", err)
	}
	got, err := Open(buf.Bytes())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if got.Len() != db.Len() {
		t.Errorf("Len = %d, want %d", got.Len(), db.Len())
	}
	if !got.Generated().Equal(db.Generated()) {
		t.Errorf("Generated = %v, want %v", got.Generated(), db.Generated())
	}
	// The indexes must survive the round trip, not just the entry slice.
	if _, st := got.Lookup("8C1F64AFA001"); st != MatchVendor {
		t.Errorf("lookup after round trip: status = %q, want %q", st, MatchVendor)
	}
	if _, st := got.LookupCID("EA2701000001"); st != MatchVendor {
		t.Errorf("CID lookup after round trip: status = %q, want %q", st, MatchVendor)
	}
}

func TestSearch(t *testing.T) {
	db := testDB(t)
	// Case-insensitive substring, across registries: "QUERCUS TECHNOLOGIES"
	// (MA-S) and "ACCE Technology Corp." (CID) both match.
	got, total := db.Search("technolog", 0)
	if total != 2 || len(got) != 2 {
		t.Fatalf("got %d/%d results, want 2/2", len(got), total)
	}
	// Results keep the deterministic entry order (sorted by assignment).
	if got[0].Organization != "QUERCUS TECHNOLOGIES, S.L." || got[1].Organization != "ACCE Technology Corp." {
		t.Errorf("results = %q, %q", got[0].Organization, got[1].Organization)
	}
	// The limit truncates the returned rows but the total still reports the full
	// count, so a caller can say how much it is not showing.
	got, total = db.Search("IEEE", 1)
	if len(got) != 1 || total != 2 {
		t.Errorf("got %d rows / total %d, want 1 / 2", len(got), total)
	}
	if got, total := db.Search("  ", 0); got != nil || total != 0 {
		t.Errorf("blank query returned %d rows / total %d, want nothing", len(got), total)
	}
}

func TestCountsByRegistry(t *testing.T) {
	counts := testDB(t).CountsByRegistry()
	want := map[Registry]int{RegistryMAL: 4, RegistryMAS: 2, RegistryMAM: 1, RegistryCID: 1}
	for reg, n := range want {
		if counts[reg] != n {
			t.Errorf("counts[%q] = %d, want %d", reg, counts[reg], n)
		}
	}
}
