// Package mcp implements the local stdio MCP server: a zero-dependency
// JSON-RPC 2.0 loop over the shared engine.
//
// Scaffold: the tool names are fixed here; the server and the tool handlers
// land in Phase 3.
package mcp

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
	// ToolGetUsage returns the embedded operating manual (usage.md), advertised
	// via the initialize instructions field.
	ToolGetUsage = "get_usage"
)
