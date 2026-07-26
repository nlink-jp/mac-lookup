package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nlink-jp/mac-lookup/internal/engine"
	"github.com/nlink-jp/mac-lookup/internal/macaddr"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// usageMarkdown is the operating manual returned by the get_usage tool. Its
// coherence with the real tools/results is pinned by usage_test.go.
//
//go:embed usage.md
var usageMarkdown string

// Tool names exposed to MCP clients. They mirror the asn-lookup and
// tor-exit-lookup naming so an agent that knows one knows the others.
const (
	// ToolLookupMAC resolves a MAC address, BSSID or prefix.
	ToolLookupMAC = "lookup_mac"
	// ToolSearchVendor reverse-resolves a vendor name to its assignments. A
	// popular vendor holds hundreds of prefixes, so the result is file-mediated:
	// the caller passes workspace_root and reads the returned matches_file.
	ToolSearchVendor = "search_vendor"
	// ToolDBStatus reports the cached registry's freshness and size.
	ToolDBStatus = "db_status"
	// ToolUpdateDB refetches the registries.
	ToolUpdateDB = "update_db"
	// ToolGetUsage returns the embedded operating manual.
	ToolGetUsage = "get_usage"
)

// Instructions is the initialize-time hint (surfaced via the MCP `instructions`
// field) that makes get_usage discoverable and steers clients away from the one
// mistake that matters most here.
const Instructions = "mac-lookup resolves a MAC address or BSSID to its manufacturer and to the kind of address it is, fully offline from a locally cached copy of the IEEE registries. " +
	"Call db_status first; if there is no cache, call update_db (no credentials needed). " +
	"Read vendor_lookup_applicable before reading vendor: when it is false the address is broadcast, multicast, or locally administered (a randomized MAC or virtual NIC), and no manufacturer exists to find — that is an answer, not a failed lookup. " +
	"Call get_usage for the full tool reference and error-recovery table."

// toolsList returns the advertised tool set with JSON Schema for each input.
func (s *server) toolsList() any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        ToolGetUsage,
				"description": "Return this server's operating manual (markdown): the tools, the offline registry lifecycle, how to read a result, and the error-recovery table. Call it once before first use.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"name": ToolLookupMAC,
				"description": "Resolve one or more MAC addresses, BSSIDs, or registry prefixes, answered offline from the cached IEEE registries. " +
					"Returns the address classification (cast, administration, well_known) and, when applicable, the matched assignment. " +
					"Check vendor_lookup_applicable: when false there is no manufacturer to find.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"mac":  map[string]any{"type": "string", "description": "A single MAC address, BSSID, or 24/28/36-bit prefix. Colon, hyphen, dot, or bare hex."},
						"macs": strArray,
					},
				},
			},
			{
				"name": ToolSearchVendor,
				"description": "Find the IEEE assignments whose registrant name contains a substring (case-insensitive). " +
					"File-mediated: results are written as JSON Lines under workspace_root and the path is returned as matches_file, because a large vendor holds hundreds of prefixes.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":          map[string]any{"type": "string", "description": "Substring of the registrant name, e.g. \"Apple\"."},
						"workspace_root": map[string]any{"type": "string", "description": "Writable directory for the results file. Defaults to the configured workspace."},
						"limit":          map[string]any{"type": "integer", "description": "Maximum rows to write (0 = no limit)."},
					},
					"required": []string{"query"},
				},
			},
			{
				"name":        ToolUpdateDB,
				"description": "Download the five IEEE Registration Authority registry files (MA-L, MA-M, MA-S, IAB, CID) and rebuild the local store. Conditional, so unchanged files cost nothing. No credentials required.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"name":        ToolDBStatus,
				"description": "Report the cached registry's generation time, assignment count per registry, sources, and whether it is stale.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

func (s *server) toolsCall(ctx context.Context, params json.RawMessage) (toolResult, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolResult{}, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	switch p.Name {
	case ToolGetUsage:
		return textResult(false, usageMarkdown), nil
	case ToolLookupMAC:
		return s.toolLookupMAC(p.Arguments), nil
	case ToolSearchVendor:
		return s.toolSearchVendor(p.Arguments), nil
	case ToolUpdateDB:
		return s.toolUpdateDB(ctx), nil
	case ToolDBStatus:
		return s.toolDBStatus(), nil
	default:
		return toolResult{}, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
}

// matchEntry is the assignment behind a lookup_mac result.
type matchEntry struct {
	Registry     ouidb.Registry `json:"registry"`
	Assignment   string         `json:"assignment"`
	PrefixBits   int            `json:"prefix_bits"`
	Organization string         `json:"organization"`
	Address      string         `json:"address"`
	Private      bool           `json:"private"`
}

// lookupEntry is the per-address result of lookup_mac.
type lookupEntry struct {
	Input                  string            `json:"input"`
	MAC                    string            `json:"mac,omitempty"`
	Bits                   int               `json:"bits,omitempty"`
	Cast                   string            `json:"cast,omitempty"`
	Administration         string            `json:"administration,omitempty"`
	VendorLookupApplicable bool              `json:"vendor_lookup_applicable"`
	Status                 ouidb.MatchStatus `json:"status,omitempty"`
	Vendor                 string            `json:"vendor,omitempty"`
	Match                  *matchEntry       `json:"match"`
	WellKnown              string            `json:"well_known,omitempty"`
	Note                   string            `json:"note,omitempty"`
	Error                  string            `json:"error,omitempty"`
}

// note explains a result that carries no vendor name. An agent that only reads
// `vendor` would treat every one of these the same way; they are not the same.
func note(r engine.Result) string {
	switch {
	case r.Address.Cast == macaddr.CastBroadcast:
		return "broadcast address; no manufacturer is involved"
	case r.Address.Cast == macaddr.CastMulticast:
		return "multicast address; it identifies a group, not a device"
	case r.Address.Administration == macaddr.AdminLocal && r.Status == engine.StatusNotApplicable:
		return "locally administered: a randomized MAC, a virtual NIC, or a container interface. There is no IEEE assignment to find — do not treat this address as identifying a device model, and do not correlate it across networks"
	case r.Address.Administration == macaddr.AdminLocal && r.HasEntry:
		// vendor_lookup_applicable is false here — the OUI registries do not
		// cover this address — yet a vendor name is present, because CID exists
		// precisely to assign locally administered prefixes. Say so, or the two
		// fields look like they contradict each other.
		return "locally administered, but matched a CID assignment: the prefix identifies the organization that assigned it, not a stable device identity. The OUI registries do not cover this address, so do not correlate it across networks"
	case r.Status == ouidb.MatchPrivate:
		return "assigned, but the registrant withheld their name from the public registry"
	case r.Status == ouidb.MatchSubdivided && r.Address.Bits < 48:
		return "this OUI is subdivided into smaller assignments; supply the full 48-bit address to resolve the vendor"
	case r.Status == ouidb.MatchSubdivided:
		return "this OUI is subdivided into smaller assignments and none covers this address"
	case r.Status == ouidb.MatchNotFound:
		return "no IEEE assignment covers this address"
	}
	return ""
}

func (s *server) toolLookupMAC(args json.RawMessage) toolResult {
	var a struct {
		MAC  string   `json:"mac"`
		MACs []string `json:"macs"`
	}
	_ = json.Unmarshal(args, &a)
	inputs := a.MACs
	if a.MAC != "" {
		inputs = append([]string{a.MAC}, inputs...)
	}
	if len(inputs) == 0 {
		return textResult(true, "provide 'mac' (string) or 'macs' (array of strings)")
	}
	db, err := s.database()
	if err != nil {
		return dbErrorResult(err)
	}

	entries := make([]lookupEntry, 0, len(inputs))
	for _, in := range inputs {
		r, rerr := engine.Resolve(db, in)
		if rerr != nil {
			entries = append(entries, lookupEntry{Input: in, Error: "invalid address: expected 12 hex digits (full address) or 6/7/9 (24/28/36-bit prefix)"})
			continue
		}
		le := lookupEntry{
			Input:                  in,
			MAC:                    r.Address.Canonical(),
			Bits:                   r.Address.Bits,
			Cast:                   string(r.Address.Cast),
			Administration:         string(r.Address.Administration),
			VendorLookupApplicable: r.Address.VendorLookupApplicable(),
			Status:                 r.Status,
			Vendor:                 r.VendorName(),
			Note:                   note(r),
		}
		if r.HasEntry {
			le.Match = &matchEntry{
				Registry:     r.Entry.Registry,
				Assignment:   r.Entry.Assignment,
				PrefixBits:   r.Entry.PrefixBits,
				Organization: r.Entry.Organization,
				Address:      r.Entry.Address,
				Private:      r.Entry.Private(),
			}
		}
		if r.HasWellKnown {
			le.WellKnown = r.WellKnown.Name
		}
		entries = append(entries, le)
	}
	return jsonResult(entries)
}

func (s *server) toolSearchVendor(args json.RawMessage) toolResult {
	var a struct {
		Query         string `json:"query"`
		WorkspaceRoot string `json:"workspace_root"`
		Limit         int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &a)
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return textResult(true, "provide 'query' (a substring of the registrant name)")
	}
	db, err := s.database()
	if err != nil {
		return dbErrorResult(err)
	}

	root := a.WorkspaceRoot
	if root == "" {
		root = s.e.Cfg.Workspace
	}
	if root == "" {
		return textResult(true, "no workspace available: pass 'workspace_root' (a writable directory) or configure [workspace] path")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return textResult(true, "create workspace "+root+": "+err.Error())
	}

	matches, total := engine.SearchVendor(db, query, a.Limit)
	path := filepath.Join(root, "search-"+slug(query)+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return textResult(true, "write results: "+err.Error())
	}
	enc := json.NewEncoder(f)
	for _, m := range matches {
		if err := enc.Encode(map[string]any{
			"registry":     m.Registry,
			"assignment":   m.Assignment,
			"prefix":       macaddr.Canonical(m.Assignment),
			"prefix_bits":  m.PrefixBits,
			"organization": m.Organization,
			"address":      m.Address,
		}); err != nil {
			f.Close()
			return textResult(true, "write results: "+err.Error())
		}
	}
	if err := f.Close(); err != nil {
		return textResult(true, "write results: "+err.Error())
	}

	out := map[string]any{
		"query":        query,
		"total":        total,
		"written":      len(matches),
		"matches_file": path,
		"format":       "JSON Lines: one assignment per line",
	}
	// A silently truncated list would read as the complete answer.
	if total > len(matches) {
		out["truncated"] = true
		out["note"] = fmt.Sprintf("%d of %d matches written; raise 'limit' to get the rest", len(matches), total)
	}
	return jsonResult(out)
}

// slug makes a query safe to use as a filename component without colliding with
// path separators or hidden files.
func slug(q string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(q) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "query"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func (s *server) toolUpdateDB(ctx context.Context) toolResult {
	res, err := s.e.Update(ctx)
	if err != nil {
		return textResult(true, "update failed: "+err.Error())
	}
	counts := map[string]int{}
	for reg, n := range res.Counts {
		counts[string(reg)] = n
	}
	out := map[string]any{
		"updated":     true,
		"generated":   res.Generated,
		"assignments": res.Total,
		"registries":  counts,
		"downloaded":  res.Downloaded,
		"path":        res.StorePath,
	}
	if len(res.NotModified) > 0 {
		out["not_modified"] = res.NotModified
	}
	// "Unchanged upstream" and "IEEE was unreachable, serving old data" both
	// leave the cache in place but mean very different things.
	if len(res.Fallback) > 0 {
		out["served_from_cache_after_failure"] = res.Fallback
	}
	if len(res.Warnings) > 0 {
		out["warnings"] = res.Warnings
	}
	return jsonResult(out)
}

func (s *server) toolDBStatus() toolResult {
	db, err := s.database()
	if err != nil {
		return dbErrorResult(err)
	}
	counts := map[string]int{}
	for reg, n := range db.CountsByRegistry() {
		counts[string(reg)] = n
	}
	stale, age := s.e.IsStale(db.Generated())
	return jsonResult(map[string]any{
		"generated":   db.Generated(),
		"assignments": db.Len(),
		"registries":  counts,
		"stale":       stale,
		"age_hours":   int(age.Hours()),
		"sources":     db.Sources,
		"path":        s.e.Cfg.StorePath,
	})
}

// jsonResult marshals v into a non-error text result.
func jsonResult(v any) toolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textResult(true, "encode result: "+err.Error())
	}
	return textResult(false, string(b))
}

// dbErrorResult renders a store load error, adding an update hint when no
// registry has been downloaded yet.
func dbErrorResult(err error) toolResult {
	msg := err.Error()
	if errors.Is(err, engine.ErrNoDB) {
		msg += "\nCall the update_db tool to download the IEEE registries."
	}
	return textResult(true, msg)
}
