package app

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nlink-jp/mac-lookup/internal/engine"
	"github.com/nlink-jp/mac-lookup/internal/macaddr"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// matchJSON is the registry assignment behind a result.
type matchJSON struct {
	Registry     ouidb.Registry `json:"registry"`
	Assignment   string         `json:"assignment"`
	PrefixBits   int            `json:"prefix_bits"`
	Organization string         `json:"organization"`
	// Address is the registrant address exactly as IEEE wrote it. It is free
	// text with inconsistent formatting, so nothing is parsed out of it.
	Address string `json:"address"`
	Private bool   `json:"private"`
}

type wellKnownJSON struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
}

// lookupJSON is one line of `lookup --json` output.
type lookupJSON struct {
	Input string `json:"input"`
	MAC   string `json:"mac,omitempty"`
	Bits  int    `json:"bits,omitempty"`
	Cast  string `json:"cast,omitempty"`
	// Administration is who assigned the address: "universal" or "local".
	Administration string `json:"administration,omitempty"`
	// VendorLookupApplicable reports whether the OUI registries can say anything
	// about this address at all. When false, an absent vendor means "there is
	// nothing to find", not "the lookup missed".
	VendorLookupApplicable bool              `json:"vendor_lookup_applicable"`
	Status                 ouidb.MatchStatus `json:"status,omitempty"`
	Vendor                 string            `json:"vendor,omitempty"`
	Match                  *matchJSON        `json:"match"`
	WellKnown              *wellKnownJSON    `json:"well_known"`
	Note                   string            `json:"note,omitempty"`
	Error                  string            `json:"error,omitempty"`
}

// noteFor explains a result that carries no vendor name, so the reader does not
// have to infer the difference between the several ways that happens.
func noteFor(r engine.Result) string {
	switch {
	case r.Address.Cast == macaddr.CastBroadcast:
		return "broadcast address; no manufacturer is involved"
	case r.Address.Cast == macaddr.CastMulticast:
		return "multicast address; it identifies a group, not a device"
	case r.Address.Administration == macaddr.AdminLocal && r.Status == engine.StatusNotApplicable:
		return "locally administered: a randomized MAC, a virtual NIC, or a container interface. There is no IEEE assignment to find — this address does not identify a device model"
	case r.Address.Administration == macaddr.AdminLocal && r.HasEntry:
		// vendor_lookup_applicable is false here — the OUI registries do not
		// cover this address — yet a vendor name is present, because CID exists
		// precisely to assign locally administered prefixes. Say so, or the two
		// fields look like they contradict each other.
		return "locally administered, but matched a CID assignment: the prefix identifies the organization that assigned it, not a stable device identity. The OUI registries do not cover this address"
	case r.Status == ouidb.MatchPrivate:
		return "assigned, but the registrant withheld their name from the public registry"
	case r.Status == ouidb.MatchSubdivided && r.Address.Bits < 48:
		return "this OUI is subdivided into smaller assignments; supply the full address to resolve the vendor"
	case r.Status == ouidb.MatchSubdivided:
		return "this OUI is subdivided into smaller assignments and none covers this address"
	case r.Status == ouidb.MatchNotFound:
		return "no IEEE assignment covers this address"
	}
	return ""
}

func toJSON(r engine.Result) lookupJSON {
	out := lookupJSON{
		Input:                  r.Address.Input,
		MAC:                    r.Address.Canonical(),
		Bits:                   r.Address.Bits,
		Cast:                   string(r.Address.Cast),
		Administration:         string(r.Address.Administration),
		VendorLookupApplicable: r.Address.VendorLookupApplicable(),
		Status:                 r.Status,
		Vendor:                 r.VendorName(),
		Note:                   noteFor(r),
	}
	if r.HasEntry {
		out.Match = &matchJSON{
			Registry:     r.Entry.Registry,
			Assignment:   r.Entry.Assignment,
			PrefixBits:   r.Entry.PrefixBits,
			Organization: r.Entry.Organization,
			Address:      r.Entry.Address,
			Private:      r.Entry.Private(),
		}
	}
	if r.HasWellKnown {
		out.WellKnown = &wellKnownJSON{Prefix: r.WellKnown.Prefix, Name: r.WellKnown.Name}
	}
	return out
}

// writeJSONLine emits one JSON object per line, so a batch streams and stays
// greppable.
func writeJSONLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

// writeText renders one result as a single line. The vendor name is the only
// thing printed when there is one; every other outcome is spelled out, because
// a blank column would read as a failed lookup.
func writeText(w io.Writer, r engine.Result) {
	label := r.VendorName()
	switch {
	case label != "":
		// A reserved range can sit inside an ordinary assignment: VRRP virtual
		// routers live under IANA's OUI, so resolving the vendor and stopping
		// would report "ICANN, IANA Department" for what is really a virtual
		// router address. Say both.
		suffix := ""
		if r.HasWellKnown {
			suffix = "  (" + r.WellKnown.Name + ")"
		}
		if r.Address.Administration == macaddr.AdminLocal {
			// A CID match names the assigner of a locally administered prefix.
			// Without this marker the line is indistinguishable from an ordinary
			// OUI hit, and the address would look like a stable device identity.
			suffix += "  (locally administered)"
		}
		fmt.Fprintf(w, "%s  %s  [%s /%d]%s\n", r.Address.Canonical(), label, r.Entry.Registry, r.Entry.PrefixBits, suffix)
		return
	case r.HasWellKnown:
		fmt.Fprintf(w, "%s  (%s — %s)\n", r.Address.Canonical(), r.Address.Cast, r.WellKnown.Name)
		return
	case r.Status == ouidb.MatchPrivate:
		fmt.Fprintf(w, "%s  (Private — registered, name withheld)  [%s /%d]\n", r.Address.Canonical(), r.Entry.Registry, r.Entry.PrefixBits)
		return
	case r.Status == ouidb.MatchSubdivided:
		fmt.Fprintf(w, "%s  (OUI subdivided — no assignment covers this address)\n", r.Address.Canonical())
		return
	case r.Address.Administration == macaddr.AdminLocal:
		fmt.Fprintf(w, "%s  (locally administered — randomized MAC or virtual NIC; vendor lookup does not apply)\n", r.Address.Canonical())
		return
	default:
		fmt.Fprintf(w, "%s  (no assignment found)\n", r.Address.Canonical())
	}
}

// searchJSON is one line of `search --json` output.
type searchJSON struct {
	Registry     ouidb.Registry `json:"registry"`
	Assignment   string         `json:"assignment"`
	Prefix       string         `json:"prefix"`
	PrefixBits   int            `json:"prefix_bits"`
	Organization string         `json:"organization"`
	Address      string         `json:"address"`
}

func toSearchJSON(e ouidb.Entry) searchJSON {
	return searchJSON{
		Registry:     e.Registry,
		Assignment:   e.Assignment,
		Prefix:       macaddr.Canonical(e.Assignment),
		PrefixBits:   e.PrefixBits,
		Organization: e.Organization,
		Address:      e.Address,
	}
}
