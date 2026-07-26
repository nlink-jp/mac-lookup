package mcp

import (
	"strings"
	"testing"

	"github.com/nlink-jp/mac-lookup/internal/engine"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// usage.md is the manual an agent reads before its first call. Pinning it here
// means renaming a tool, adding a status, or changing a documented field breaks
// the build instead of silently leaving the manual wrong.

func TestUsageDocumentsEveryTool(t *testing.T) {
	for _, name := range []string{ToolGetUsage, ToolLookupMAC, ToolSearchVendor, ToolUpdateDB, ToolDBStatus} {
		if !strings.Contains(usageMarkdown, name) {
			t.Errorf("usage.md does not document the %q tool", name)
		}
	}
}

func TestUsageDocumentsEveryStatus(t *testing.T) {
	statuses := []ouidb.MatchStatus{
		ouidb.MatchVendor, ouidb.MatchPrivate, ouidb.MatchSubdivided,
		ouidb.MatchNotFound, engine.StatusNotApplicable,
	}
	for _, st := range statuses {
		if !strings.Contains(usageMarkdown, string(st)) {
			t.Errorf("usage.md does not explain the %q status", st)
		}
	}
}

func TestUsageDocumentsResultFields(t *testing.T) {
	// Every field a caller is expected to branch on.
	for _, field := range []string{
		"vendor_lookup_applicable", "administration", "prefix_bits",
		"matches_file", "workspace_root", "not_modified",
		"served_from_cache_after_failure",
	} {
		if !strings.Contains(usageMarkdown, field) {
			t.Errorf("usage.md does not document the %q field", field)
		}
	}
}

func TestUsageLeadsWithTheDangerousMisreading(t *testing.T) {
	// The whole point of the tool is that an empty vendor has several meanings.
	// If the manual ever stops saying so, an agent will start treating a
	// randomized MAC as an unidentified device.
	for _, phrase := range []string{"randomized MAC", "locally administered", "not mean the lookup missed"} {
		if !strings.Contains(usageMarkdown, phrase) {
			t.Errorf("usage.md no longer warns about %q", phrase)
		}
	}
}

func TestUsageDocumentsTheTeapot(t *testing.T) {
	// The 418 is the single most confusing failure this tool can produce.
	if !strings.Contains(usageMarkdown, "418") {
		t.Error("usage.md does not cover the HTTP 418 failure")
	}
}
