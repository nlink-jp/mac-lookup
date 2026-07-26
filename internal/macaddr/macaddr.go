// Package macaddr parses MAC addresses and BSSIDs and classifies what kind of
// address they are. It is pure: it knows nothing about the IEEE registries and
// never touches the network or the filesystem.
//
// Classification runs *before* any vendor lookup, and that ordering is the
// point of this tool. A locally administered address (a randomized MAC from
// iOS/Android, a virtual NIC, a container interface) has no manufacturer to
// find — reporting it as "vendor unknown" instead of "vendor lookup does not
// apply" is what leads investigators to misidentify a device.
package macaddr

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid means the input is not a MAC address, BSSID, or registry prefix.
var ErrInvalid = errors.New("invalid MAC address")

// Cast describes the delivery semantics carried by the I/G bit (bit 0 of the
// first octet), plus the all-ones broadcast special case.
type Cast string

const (
	CastUnicast   Cast = "unicast"
	CastMulticast Cast = "multicast"
	CastBroadcast Cast = "broadcast"
)

// Administration describes who assigned the address, carried by the U/L bit
// (bit 1 of the first octet).
type Administration string

const (
	// AdminUniversal means the address came from an IEEE assignment, so a
	// registry lookup is meaningful.
	AdminUniversal Administration = "universal"
	// AdminLocal means the address was assigned locally: a randomized MAC, a
	// virtual NIC, a container interface. An OUI lookup is meaningless; only the
	// CID registry can say anything, and only rarely.
	AdminLocal Administration = "local"
)

// Address is a parsed MAC address, BSSID, or bare prefix.
type Address struct {
	// Input is the caller's original text, echoed back unchanged.
	Input string
	// Hex is the address as upper-case hex with no separators. A full address is
	// 12 characters; a prefix is 6, 7, or 9 (24, 28, or 36 bits). Assignment
	// widths are not byte-aligned, so the address is held as nibbles, not bytes.
	Hex string
	// Bits is the number of significant bits supplied.
	Bits int
	// Cast and Administration are derived from the first octet.
	Cast           Cast
	Administration Administration
}

// validBits are the input widths that mean something: a full 48-bit address, or
// a bare prefix at one of the three IEEE assignment widths.
var validBits = map[int]bool{24: true, 28: true, 36: true, 48: true}

// Parse normalizes and classifies an address. It accepts colon-, hyphen-, and
// dot-separated forms as well as bare hex, in any case, and accepts a prefix at
// 24, 28, or 36 bits in addition to a full 48-bit address.
//
// Separator placement is not validated: operators paste addresses from capture
// tools, log lines, and vendor UIs that all group them differently, and
// rejecting an unusual grouping would only obstruct the lookup. The hex digits
// and their count are what carry meaning.
func Parse(input string) (Address, error) {
	var b strings.Builder
	b.Grow(len(input))
	for _, r := range input {
		switch {
		case r == ':' || r == '-' || r == '.' || r == ' ' || r == '\t':
			continue
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
			b.WriteRune(r)
		case r >= 'a' && r <= 'f':
			b.WriteRune(r - 'a' + 'A')
		default:
			return Address{}, fmt.Errorf("%w %q: unexpected character %q", ErrInvalid, input, r)
		}
	}
	hex := b.String()
	bits := len(hex) * 4
	if !validBits[bits] {
		return Address{}, fmt.Errorf("%w %q: got %d hex digits, want 12 (full address) or 6/7/9 (24/28/36-bit prefix)", ErrInvalid, input, len(hex))
	}

	a := Address{Input: input, Hex: hex, Bits: bits}
	// The first octet carries both classification bits: I/G is bit 0, U/L is
	// bit 1. Every accepted width includes it.
	first := hexPairValue(hex)
	switch {
	case bits == 48 && hex == "FFFFFFFFFFFF":
		a.Cast = CastBroadcast
	case first&0x01 != 0:
		a.Cast = CastMulticast
	default:
		a.Cast = CastUnicast
	}
	if first&0x02 != 0 {
		a.Administration = AdminLocal
	} else {
		a.Administration = AdminUniversal
	}
	return a, nil
}

// hexPairValue returns the numeric value of the first two hex digits.
func hexPairValue(hex string) byte {
	return hexVal(hex[0])<<4 | hexVal(hex[1])
}

func hexVal(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	return c - 'A' + 10
}

// Canonical renders the address colon-separated in upper case. A 28-bit prefix
// ends in a single nibble (for example "C8:5C:E2:7"), which is how IEEE itself
// writes those assignments.
func Canonical(hex string) string {
	var b strings.Builder
	for i := 0; i < len(hex); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		if i+2 <= len(hex) {
			b.WriteString(hex[i : i+2])
		} else {
			b.WriteByte(hex[i])
		}
	}
	return b.String()
}

// Canonical renders this address colon-separated in upper case.
func (a Address) Canonical() string { return Canonical(a.Hex) }

// VendorLookupApplicable reports whether consulting the OUI registries can say
// anything about this address. It is false for broadcast and multicast (no
// manufacturer is involved) and for locally administered addresses (no IEEE
// assignment exists). Callers must surface this rather than letting an empty
// vendor read as "not found" — the two lead to opposite conclusions about
// whether the device can be identified at all.
func (a Address) VendorLookupApplicable() bool {
	return a.Cast == CastUnicast && a.Administration == AdminUniversal
}

// WellKnown names a reserved or protocol-assigned address range, so that
// control-plane traffic is reported as what it is rather than as an unresolved
// vendor prefix.
type WellKnown struct {
	Prefix string // canonical upper-case hex prefix, colon-separated
	Name   string
}

// wellKnownPrefixes is consulted for multicast and reserved addresses, longest
// prefix first. It is deliberately short: only ranges an operator is likely to
// meet in a capture or a wireless survey.
//
// Some of these are unicast (VRRP virtual routers live under IANA's 00:00:5E
// OUI), so the table is consulted for every address, not just multicast ones.
var wellKnownPrefixes = []WellKnown{
	{Prefix: "01:80:C2:00:00:0E", Name: "PTP peer delay (IEEE 1588 / 802.1AS)"},
	{Prefix: "00:00:5E:00:01", Name: "VRRP (IPv4, RFC 5798)"},
	{Prefix: "00:00:5E:00:02", Name: "VRRP (IPv6, RFC 5798)"},
	{Prefix: "01:1B:19", Name: "PTP (IEEE 1588)"},
	{Prefix: "01:80:C2", Name: "IEEE 802.1D reserved (STP, LLDP, PAUSE, ...)"},
	{Prefix: "01:00:5E", Name: "IPv4 multicast (RFC 1112)"},
	{Prefix: "01:00:0C", Name: "Cisco CDP / VTP / PVST+"},
	{Prefix: "33:33", Name: "IPv6 multicast (RFC 2464)"},
}

// WellKnown returns the most specific reserved range covering this address.
func (a Address) WellKnown() (WellKnown, bool) {
	if a.Cast == CastBroadcast {
		return WellKnown{Prefix: "FF:FF:FF:FF:FF:FF", Name: "Broadcast"}, true
	}
	best := -1
	bestLen := 0
	for i, wk := range wellKnownPrefixes {
		p := strings.ReplaceAll(wk.Prefix, ":", "")
		if len(p) > len(a.Hex) || !strings.HasPrefix(a.Hex, p) {
			continue
		}
		if len(p) > bestLen {
			best, bestLen = i, len(p)
		}
	}
	if best < 0 {
		return WellKnown{}, false
	}
	return wellKnownPrefixes[best], true
}
