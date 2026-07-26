// Package macaddr parses MAC addresses and BSSIDs and classifies what kind of
// address they are. It is pure: it knows nothing about the IEEE registries and
// never touches the network or the filesystem.
//
// Classification runs *before* any vendor lookup, and that ordering is the
// point of this tool. A locally administered address (a randomized MAC from
// iOS/Android, a virtual NIC, a container interface) has no manufacturer to
// find — reporting it as "vendor unknown" instead of "vendor lookup does not
// apply" is what leads investigators to misidentify a device.
//
// Scaffold: types and the well-known address table are fixed here; parsing and
// classification are implemented in Phase 3.
package macaddr

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
	// virtual NIC, a container interface. A registry lookup is meaningless.
	AdminLocal Administration = "local"
)

// Address is a parsed MAC address, BSSID, or bare prefix.
type Address struct {
	// Input is the caller's original text, echoed back unchanged.
	Input string
	// Bytes holds the parsed octets, most significant first. A full address is
	// 6 bytes; a prefix may be shorter.
	Bytes []byte
	// Bits is the number of significant bits supplied (48 for a full address,
	// 24/28/36 for a prefix).
	Bits int
	// Cast and Administration are derived from the first octet.
	Cast           Cast
	Administration Administration
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
