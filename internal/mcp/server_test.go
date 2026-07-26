package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/mac-lookup/internal/config"
	"github.com/nlink-jp/mac-lookup/internal/engine"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// --- harness ----------------------------------------------------------------

// newServer returns an engine whose store is either absent or seeded with a
// small registry, plus the temp directory holding it.
func newServer(t *testing.T, seeded bool) (*engine.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		BaseURL:   "https://registry.invalid",
		StorePath: filepath.Join(dir, "ouidb.json"),
		Workspace: filepath.Join(dir, "workspace"),
		TTL:       config.DefaultTTL,
	}
	if seeded {
		db := ouidb.New([]ouidb.Entry{
			{Registry: ouidb.RegistryMAL, Assignment: "286FB9", PrefixBits: 24, Organization: "Nokia Shanghai Bell Co., Ltd.", Address: "Shanghai CN"},
			{Registry: ouidb.RegistryMAL, Assignment: "8C1F64", PrefixBits: 24, Organization: ouidb.RegistryHeldOrganization},
			{Registry: ouidb.RegistryMAS, Assignment: "8C1F64AFA", PrefixBits: 36, Organization: "DATA ELECTRONIC DEVICES, INC"},
			{Registry: ouidb.RegistryCID, Assignment: "EA2701", PrefixBits: 24, Organization: "ACCE Technology Corp."},
		}, []ouidb.Source{{Registry: ouidb.RegistryMAL, URL: "https://registry.invalid/oui/oui.csv", Count: 2}}, 1753488000)

		f, err := os.Create(cfg.StorePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := ouidb.Serialize(f, db); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return engine.New(cfg, nil), dir
}

// rpc drives the server the way a real MCP client would: newline-delimited
// JSON-RPC on stdin, one response object per request on stdout.
func rpc(t *testing.T, e *engine.Engine, requests ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	if err := Serve(context.Background(), e, "test", in, &out); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	var got []map[string]any
	dec := json.NewDecoder(&out)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		got = append(got, m)
	}
	return got
}

// callText returns the text payload of a tools/call result, and whether the
// server flagged it as an error.
func callText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result: %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result has no content: %v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func call(name string, args string) string {
	if args == "" {
		args = "{}"
	}
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

// --- protocol ---------------------------------------------------------------

func TestInitializeAdvertisesUsage(t *testing.T) {
	e, _ := newServer(t, false)
	got := rpc(t, e, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
	result := got[0]["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "mac-lookup" {
		t.Errorf("serverInfo.name = %v", info["name"])
	}
	instructions, _ := result["instructions"].(string)
	// The instructions are the only thing a client is guaranteed to read, so
	// the one dangerous misreading must be pre-empted there.
	for _, want := range []string{"get_usage", "vendor_lookup_applicable", "db_status"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("instructions omit %q", want)
		}
	}
}

func TestNotificationsGetNoReply(t *testing.T) {
	e, _ := newServer(t, false)
	got := rpc(t, e, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(got) != 0 {
		t.Errorf("got %d responses to a notification, want 0", len(got))
	}
}

func TestUnknownMethodAndTool(t *testing.T) {
	e, _ := newServer(t, false)
	got := rpc(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"nope"}`,
		call("no_such_tool", ""),
	)
	if len(got) != 2 {
		t.Fatalf("got %d responses, want 2", len(got))
	}
	for i, resp := range got {
		if resp["error"] == nil {
			t.Errorf("response %d has no error: %v", i, resp)
		}
	}
}

func TestMalformedJSONStopsCleanly(t *testing.T) {
	e, _ := newServer(t, false)
	// The stream position is unrecoverable after bad JSON, so the server must
	// report a parse error and stop rather than spin.
	got := rpc(t, e, `{"jsonrpc":"2.0","id":1,`)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
	rerr, ok := got[0]["error"].(map[string]any)
	if !ok || rerr["code"].(float64) != -32700 {
		t.Errorf("error = %v, want a -32700 parse error", got[0]["error"])
	}
}

func TestToolsListCoversEveryTool(t *testing.T) {
	e, _ := newServer(t, false)
	got := rpc(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := got[0]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{ToolGetUsage, ToolLookupMAC, ToolSearchVendor, ToolUpdateDB, ToolDBStatus} {
		if !names[want] {
			t.Errorf("tools/list omits %q", want)
		}
	}
	if len(names) != 5 {
		t.Errorf("tools/list advertises %d tools, want 5", len(names))
	}
}

// --- tools ------------------------------------------------------------------

func TestLookupMACWithoutCacheHints(t *testing.T) {
	e, _ := newServer(t, false)
	text, isErr := callText(t, rpc(t, e, call(ToolLookupMAC, `{"mac":"28:6F:B9:12:34:56"}`))[0])
	if !isErr {
		t.Error("isError = false with no registry cached")
	}
	// A bare "no cache" is a dead end for an agent; the recovery must be named.
	if !strings.Contains(text, ToolUpdateDB) {
		t.Errorf("error text does not point at %s: %q", ToolUpdateDB, text)
	}
}

func TestLookupMACResolvesVendor(t *testing.T) {
	e, _ := newServer(t, true)
	text, isErr := callText(t, rpc(t, e, call(ToolLookupMAC, `{"mac":"8c:1f:64:af:a0:01"}`))[0])
	if isErr {
		t.Fatalf("isError = true: %s", text)
	}
	var entries []lookupEntry
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("result is not the documented array: %v (%s)", err, text)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	got := entries[0]
	if got.Vendor != "DATA ELECTRONIC DEVICES, INC" {
		t.Errorf("Vendor = %q", got.Vendor)
	}
	if !got.VendorLookupApplicable {
		t.Error("vendor_lookup_applicable = false for a universal unicast address")
	}
	if got.Match == nil || got.Match.PrefixBits != 36 {
		t.Error("the 36-bit assignment was not reported")
	}
}

func TestLookupMACLocallyAdministered(t *testing.T) {
	e, _ := newServer(t, true)
	text, _ := callText(t, rpc(t, e, call(ToolLookupMAC, `{"mac":"DA:A1:19:12:34:56"}`))[0])
	var entries []lookupEntry
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatal(err)
	}
	got := entries[0]
	if got.VendorLookupApplicable {
		t.Error("vendor_lookup_applicable = true for a randomized MAC")
	}
	if got.Status != engine.StatusNotApplicable {
		t.Errorf("Status = %q, want %q", got.Status, engine.StatusNotApplicable)
	}
	// An agent reading only `vendor` would treat this like a miss. The note is
	// what tells it not to keep hunting for a device.
	if !strings.Contains(got.Note, "locally administered") {
		t.Errorf("Note = %q", got.Note)
	}
}

func TestLookupMACCIDMatchIsExplained(t *testing.T) {
	e, _ := newServer(t, true)
	text, _ := callText(t, rpc(t, e, call(ToolLookupMAC, `{"mac":"EA:27:01:00:00:01"}`))[0])
	var entries []lookupEntry
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatal(err)
	}
	got := entries[0]
	// vendor is set and vendor_lookup_applicable is false at the same time.
	// Without the note an agent has no way to reconcile them.
	if got.Vendor != "ACCE Technology Corp." {
		t.Errorf("Vendor = %q, want the CID assignee", got.Vendor)
	}
	if got.VendorLookupApplicable {
		t.Error("vendor_lookup_applicable = true for a locally administered address")
	}
	if !strings.Contains(got.Note, "CID") {
		t.Errorf("Note = %q, want an explanation of the CID match", got.Note)
	}
}

func TestLookupMACSubdividedNeverNamesIEEE(t *testing.T) {
	e, _ := newServer(t, true)
	text, _ := callText(t, rpc(t, e, call(ToolLookupMAC, `{"mac":"8C:1F:64:00:00:01"}`))[0])
	var entries []lookupEntry
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatal(err)
	}
	if entries[0].Vendor != "" {
		t.Errorf("Vendor = %q, want empty for a registry-held block", entries[0].Vendor)
	}
	if entries[0].Status != ouidb.MatchSubdivided {
		t.Errorf("Status = %q, want %q", entries[0].Status, ouidb.MatchSubdivided)
	}
}

func TestLookupMACBatchAndBadInput(t *testing.T) {
	e, _ := newServer(t, true)
	text, _ := callText(t, rpc(t, e, call(ToolLookupMAC, `{"macs":["28:6F:B9:12:34:56","not-a-mac"]}`))[0])
	var entries []lookupEntry
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	// One bad address must not discard the batch.
	if entries[0].Vendor == "" {
		t.Error("the valid address was not resolved")
	}
	if entries[1].Error == "" {
		t.Error("the invalid address carries no error")
	}
}

func TestLookupMACRequiresAnArgument(t *testing.T) {
	e, _ := newServer(t, true)
	text, isErr := callText(t, rpc(t, e, call(ToolLookupMAC, `{}`))[0])
	if !isErr || !strings.Contains(text, "mac") {
		t.Errorf("missing-argument error = %q (isError=%v)", text, isErr)
	}
}

func TestSearchVendorIsFileMediated(t *testing.T) {
	e, dir := newServer(t, true)
	root := filepath.Join(dir, "ws")
	text, isErr := callText(t, rpc(t, e, call(ToolSearchVendor, `{"query":"nokia","workspace_root":"`+root+`"}`))[0])
	if isErr {
		t.Fatalf("isError = true: %s", text)
	}
	var out struct {
		Total       int    `json:"total"`
		Written     int    `json:"written"`
		MatchesFile string `json:"matches_file"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not the documented object: %v (%s)", err, text)
	}
	if out.Total != 1 || out.Written != 1 {
		t.Errorf("total=%d written=%d, want 1/1", out.Total, out.Written)
	}
	if !strings.HasPrefix(out.MatchesFile, root) {
		t.Errorf("matches_file %q is outside the requested workspace", out.MatchesFile)
	}
	// The point of file mediation is that the rows really are on disk.
	body, err := os.ReadFile(out.MatchesFile)
	if err != nil {
		t.Fatalf("matches_file is not readable: %v", err)
	}
	if !strings.Contains(string(body), "Nokia") {
		t.Errorf("matches_file lacks the match: %s", body)
	}
}

func TestSearchVendorReportsTruncation(t *testing.T) {
	e, dir := newServer(t, true)
	// A truncated list must never read as the complete answer.
	text, _ := callText(t, rpc(t, e, call(ToolSearchVendor, `{"query":"e","limit":1,"workspace_root":"`+filepath.Join(dir, "ws")+`"}`))[0])
	var out struct {
		Total     int  `json:"total"`
		Written   int  `json:"written"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Written != 1 || out.Total <= 1 || !out.Truncated {
		t.Errorf("written=%d total=%d truncated=%v", out.Written, out.Total, out.Truncated)
	}
}

func TestSearchVendorRequiresAQuery(t *testing.T) {
	e, _ := newServer(t, true)
	text, isErr := callText(t, rpc(t, e, call(ToolSearchVendor, `{"query":"  "}`))[0])
	if !isErr || !strings.Contains(text, "query") {
		t.Errorf("empty-query error = %q (isError=%v)", text, isErr)
	}
}

func TestDBStatus(t *testing.T) {
	e, _ := newServer(t, true)
	text, isErr := callText(t, rpc(t, e, call(ToolDBStatus, ""))[0])
	if isErr {
		t.Fatalf("isError = true: %s", text)
	}
	var out struct {
		Assignments int            `json:"assignments"`
		Registries  map[string]int `json:"registries"`
		Stale       bool           `json:"stale"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Assignments != 4 {
		t.Errorf("assignments = %d, want 4", out.Assignments)
	}
	if out.Registries["MA-S"] != 1 {
		t.Errorf("registries = %v", out.Registries)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Apple", "apple"},
		{"Nokia Shanghai Bell Co., Ltd.", "nokia-shanghai-bell-co---ltd"},
		// A query must never be able to escape the workspace directory.
		{"../../etc/passwd", "etc-passwd"},
		{"...", "query"},
	}
	for _, tt := range tests {
		got := slug(tt.in)
		if got != tt.want {
			t.Errorf("slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if strings.ContainsAny(got, `/\.`) {
			t.Errorf("slug(%q) = %q contains a path character", tt.in, got)
		}
	}
}
