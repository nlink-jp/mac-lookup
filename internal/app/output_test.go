package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nlink-jp/mac-lookup/internal/engine"
	"github.com/nlink-jp/mac-lookup/internal/macaddr"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

func mustParse(t *testing.T, s string) macaddr.Address {
	t.Helper()
	a, err := macaddr.Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", s, err)
	}
	return a
}

func withWellKnown(t *testing.T, r engine.Result) engine.Result {
	t.Helper()
	if wk, ok := r.Address.WellKnown(); ok {
		r.WellKnown, r.HasWellKnown = wk, true
	}
	return r
}

func TestWriteTextVendor(t *testing.T) {
	r := engine.Result{
		Address:  mustParse(t, "8c:1f:64:af:a0:01"),
		Status:   ouidb.MatchVendor,
		HasEntry: true,
		Entry: ouidb.Entry{
			Registry: ouidb.RegistryMAS, Assignment: "8C1F64AFA", PrefixBits: 36,
			Organization: "DATA ELECTRONIC DEVICES, INC",
		},
	}
	var buf bytes.Buffer
	writeText(&buf, r)
	got := buf.String()
	for _, want := range []string{"8C:1F:64:AF:A0:01", "DATA ELECTRONIC DEVICES, INC", "MA-S", "/36"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}
}

func TestWriteTextKeepsWellKnownAlongsideVendor(t *testing.T) {
	// VRRP virtual routers sit under IANA's OUI. Resolving the vendor and
	// stopping would report "ICANN, IANA Department" for an address whose whole
	// point is that it is a virtual router.
	r := withWellKnown(t, engine.Result{
		Address:  mustParse(t, "00:00:5E:00:01:2A"),
		Status:   ouidb.MatchVendor,
		HasEntry: true,
		Entry: ouidb.Entry{
			Registry: ouidb.RegistryMAL, Assignment: "00005E", PrefixBits: 24,
			Organization: "ICANN, IANA Department",
		},
	})
	var buf bytes.Buffer
	writeText(&buf, r)
	got := buf.String()
	if !strings.Contains(got, "ICANN") {
		t.Errorf("output %q lost the registrant", got)
	}
	if !strings.Contains(got, "VRRP") {
		t.Errorf("output %q lost the well-known range", got)
	}
}

func TestWriteTextNonVendorOutcomesAreSpelledOut(t *testing.T) {
	tests := []struct {
		name   string
		result engine.Result
		want   string
	}{
		{
			name:   "locally administered",
			result: engine.Result{Address: mustParse(t, "DA:A1:19:12:34:56"), Status: engine.StatusNotApplicable},
			want:   "locally administered",
		},
		{
			name:   "multicast",
			result: withWellKnown(t, engine.Result{Address: mustParse(t, "33:33:00:00:00:FB"), Status: engine.StatusNotApplicable}),
			want:   "IPv6 multicast",
		},
		{
			name:   "broadcast",
			result: withWellKnown(t, engine.Result{Address: mustParse(t, "FF:FF:FF:FF:FF:FF"), Status: engine.StatusNotApplicable}),
			want:   "Broadcast",
		},
		{
			name: "private",
			result: engine.Result{
				Address: mustParse(t, "74:1A:E0:9F:FF:FF"), Status: ouidb.MatchPrivate, HasEntry: true,
				Entry: ouidb.Entry{Registry: ouidb.RegistryMAM, Assignment: "741AE09", PrefixBits: 28, Organization: ouidb.PrivateOrganization},
			},
			want: "name withheld",
		},
		{
			name: "subdivided",
			result: engine.Result{
				Address: mustParse(t, "8C:1F:64:00:00:01"), Status: ouidb.MatchSubdivided, HasEntry: true,
				Entry: ouidb.Entry{Registry: ouidb.RegistryMAL, Assignment: "8C1F64", PrefixBits: 24, Organization: ouidb.RegistryHeldOrganization},
			},
			want: "subdivided",
		},
		{
			name:   "not found",
			result: engine.Result{Address: mustParse(t, "AC:BB:CC:DD:EE:FF"), Status: ouidb.MatchNotFound},
			want:   "no assignment found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeText(&buf, tt.result)
			got := buf.String()
			if !strings.Contains(got, tt.want) {
				t.Errorf("output %q lacks %q", got, tt.want)
			}
			// A blank column would read as a failed lookup, and the sentinel
			// organizations are not manufacturers.
			if strings.Contains(got, ouidb.RegistryHeldOrganization) {
				t.Errorf("output %q names IEEE as the manufacturer", got)
			}
		})
	}
}

func TestToJSONFlagsInapplicableLookups(t *testing.T) {
	r := engine.Result{Address: mustParse(t, "DA:A1:19:12:34:56"), Status: engine.StatusNotApplicable}
	out := toJSON(r)
	if out.VendorLookupApplicable {
		t.Error("vendor_lookup_applicable = true for a locally administered address")
	}
	if out.Vendor != "" {
		t.Errorf("Vendor = %q, want empty", out.Vendor)
	}
	// The note is what stops an empty vendor from reading as a failed lookup.
	if !strings.Contains(out.Note, "locally administered") {
		t.Errorf("Note = %q", out.Note)
	}

	// The field must be present in the encoded object, not omitted when false.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"vendor_lookup_applicable":false`)) {
		t.Errorf("encoded object omits the flag: %s", b)
	}
}

func TestCIDMatchOnALocalAddressIsExplained(t *testing.T) {
	// CID assigns locally administered prefixes, so vendor is populated while
	// vendor_lookup_applicable stays false. Those two fields look like a
	// contradiction unless the result says why.
	r := engine.Result{
		Address: mustParse(t, "DA:A1:19:12:34:56"), Status: ouidb.MatchVendor, HasEntry: true,
		Entry: ouidb.Entry{Registry: ouidb.RegistryCID, Assignment: "DAA119", PrefixBits: 24, Organization: "Google, Inc."},
	}
	out := toJSON(r)
	if out.Vendor != "Google, Inc." {
		t.Errorf("Vendor = %q, want the CID assignee", out.Vendor)
	}
	if out.VendorLookupApplicable {
		t.Error("vendor_lookup_applicable = true; the OUI registries do not cover a local address")
	}
	if !strings.Contains(out.Note, "CID") {
		t.Errorf("Note = %q, want an explanation of the CID match", out.Note)
	}

	// The text line must not look like an ordinary OUI hit either.
	var buf bytes.Buffer
	writeText(&buf, r)
	if !strings.Contains(buf.String(), "locally administered") {
		t.Errorf("text output %q does not mark the address as locally administered", buf.String())
	}
}

func TestToJSONSubdividedCarriesNoVendor(t *testing.T) {
	out := toJSON(engine.Result{
		Address: mustParse(t, "8C:1F:64"), Status: ouidb.MatchSubdivided, HasEntry: true,
		Entry: ouidb.Entry{Registry: ouidb.RegistryMAL, Assignment: "8C1F64", PrefixBits: 24, Organization: ouidb.RegistryHeldOrganization},
	})
	if out.Vendor != "" {
		t.Errorf("Vendor = %q, want empty for a registry-held block", out.Vendor)
	}
	// The raw organization stays visible under match, so nothing is hidden —
	// it just must not be presented as the vendor.
	if out.Match == nil || out.Match.Organization != ouidb.RegistryHeldOrganization {
		t.Error("match.organization was dropped")
	}
	if !strings.Contains(out.Note, "supply the full address") {
		t.Errorf("Note = %q, want the actionable hint for a bare prefix", out.Note)
	}
}

func TestToSearchJSONRendersPrefix(t *testing.T) {
	got := toSearchJSON(ouidb.Entry{
		Registry: ouidb.RegistryMAM, Assignment: "741AE09", PrefixBits: 28, Organization: "Private",
	})
	// A 28-bit assignment ends in a lone nibble, the way IEEE writes it.
	if got.Prefix != "74:1A:E0:9" {
		t.Errorf("Prefix = %q, want %q", got.Prefix, "74:1A:E0:9")
	}
}
