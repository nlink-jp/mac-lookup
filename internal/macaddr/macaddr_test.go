package macaddr

import (
	"errors"
	"testing"
)

func TestParseAcceptedForms(t *testing.T) {
	// Every notation an operator might paste in must land on the same address.
	forms := []string{
		"8C:1F:64:AF:A0:01",
		"8c:1f:64:af:a0:01",
		"8C-1F-64-AF-A0-01",
		"8c1f64afa001",
		"8C1F.64AF.A001",
		"8C 1F 64 AF A0 01",
	}
	for _, in := range forms {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", in, err)
		}
		if got.Hex != "8C1F64AFA001" {
			t.Errorf("Parse(%q).Hex = %q, want %q", in, got.Hex, "8C1F64AFA001")
		}
		if got.Bits != 48 {
			t.Errorf("Parse(%q).Bits = %d, want 48", in, got.Bits)
		}
		if got.Input != in {
			t.Errorf("Parse(%q).Input = %q, want the original text", in, got.Input)
		}
	}
}

func TestParsePrefixWidths(t *testing.T) {
	tests := []struct {
		in   string
		bits int
	}{
		{"00:11:22", 24},
		{"C8:5C:E2:7", 28},
		{"8C:1F:64:AF:A", 36},
		{"001122334455", 48},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
		}
		if got.Bits != tt.bits {
			t.Errorf("Parse(%q).Bits = %d, want %d", tt.in, got.Bits, tt.bits)
		}
	}
}

func TestParseRejects(t *testing.T) {
	// Widths that are not a full address or an IEEE assignment width carry no
	// meaning, and non-hex input is not an address at all.
	for _, in := range []string{"", "00:11", "00:11:22:33", "00112233445566", "zz:11:22:33:44:55", "8C:1F:64:AF:A0:0"} {
		if _, err := Parse(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalid", in, err)
		}
	}
}

func TestClassification(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		cast       Cast
		admin      Administration
		applicable bool
	}{
		{"universal unicast", "8C:1F:64:AF:A0:01", CastUnicast, AdminUniversal, true},
		// Bit 1 of the first octet set: a randomized MAC or a virtual NIC. There
		// is no manufacturer to find, and that is the answer, not a failure.
		{"locally administered", "8A:1F:64:AF:A0:01", CastUnicast, AdminLocal, false},
		{"random MAC style", "DA:A1:19:00:00:01", CastUnicast, AdminLocal, false},
		{"IPv4 multicast", "01:00:5E:00:00:01", CastMulticast, AdminUniversal, false},
		{"IPv6 multicast", "33:33:00:00:00:01", CastMulticast, AdminLocal, false},
		{"STP reserved", "01:80:C2:00:00:00", CastMulticast, AdminUniversal, false},
		{"broadcast", "FF:FF:FF:FF:FF:FF", CastBroadcast, AdminLocal, false},
		{"universal prefix", "00:11:22", CastUnicast, AdminUniversal, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if got.Cast != tt.cast {
				t.Errorf("Cast = %q, want %q", got.Cast, tt.cast)
			}
			if got.Administration != tt.admin {
				t.Errorf("Administration = %q, want %q", got.Administration, tt.admin)
			}
			if got.VendorLookupApplicable() != tt.applicable {
				t.Errorf("VendorLookupApplicable() = %v, want %v", got.VendorLookupApplicable(), tt.applicable)
			}
		})
	}
}

func TestBroadcastIsNotJustAnotherMulticast(t *testing.T) {
	// All-ones has the I/G bit set, so a naive check would call it multicast.
	got, err := Parse("ff:ff:ff:ff:ff:ff")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got.Cast != CastBroadcast {
		t.Errorf("Cast = %q, want %q", got.Cast, CastBroadcast)
	}
	// A 24-bit FF:FF:FF prefix is not the broadcast address.
	pre, err := Parse("FF:FF:FF")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if pre.Cast != CastMulticast {
		t.Errorf("Cast of FF:FF:FF prefix = %q, want %q", pre.Cast, CastMulticast)
	}
}

func TestWellKnown(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"FF:FF:FF:FF:FF:FF", "Broadcast"},
		{"33:33:00:00:00:FB", "IPv6 multicast (RFC 2464)"},
		{"01:00:5E:7F:00:01", "IPv4 multicast (RFC 1112)"},
		{"01:00:0C:CC:CC:CC", "Cisco CDP / VTP / PVST+"},
		// The longer 802.1AS entry must win over the 01:80:C2 range it sits in.
		{"01:80:C2:00:00:0E", "PTP peer delay (IEEE 1588 / 802.1AS)"},
		{"01:80:C2:00:00:00", "IEEE 802.1D reserved (STP, LLDP, PAUSE, ...)"},
		// VRRP virtual routers are unicast, under IANA's OUI — the table must be
		// consulted for unicast addresses too.
		{"00:00:5E:00:01:2A", "VRRP (IPv4, RFC 5798)"},
	}
	for _, tt := range tests {
		a, err := Parse(tt.in)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
		}
		wk, ok := a.WellKnown()
		if !ok {
			t.Errorf("WellKnown(%q) not found, want %q", tt.in, tt.want)
			continue
		}
		if wk.Name != tt.want {
			t.Errorf("WellKnown(%q) = %q, want %q", tt.in, wk.Name, tt.want)
		}
	}

	// An ordinary vendor address matches nothing.
	a, _ := Parse("8C:1F:64:AF:A0:01")
	if wk, ok := a.WellKnown(); ok {
		t.Errorf("WellKnown of an ordinary address = %q, want no match", wk.Name)
	}
}

func TestCanonical(t *testing.T) {
	tests := []struct{ hex, want string }{
		{"8C1F64AFA001", "8C:1F:64:AF:A0:01"},
		{"001122", "00:11:22"},
		{"C85CE27", "C8:5C:E2:7"}, // 28-bit assignments end in a lone nibble
		{"8C1F64AFA", "8C:1F:64:AF:A"},
	}
	for _, tt := range tests {
		if got := Canonical(tt.hex); got != tt.want {
			t.Errorf("Canonical(%q) = %q, want %q", tt.hex, got, tt.want)
		}
	}
}
