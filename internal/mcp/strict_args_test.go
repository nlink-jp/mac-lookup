package mcp

import (
	"strings"
	"testing"
)

// TestUnknownArgumentIsRefusedByName is the enforcing half of org ADR-021 §4:
// additionalProperties:false only tells a client what is allowed, and a client
// that does not check the schema sends the typo anyway. Every tool must refuse
// it, and the message must name the offending field — a caller that is told
// only "invalid arguments" has to re-read the schema to find its own typo.
//
// `offset` is the one that matters here: a misspelt one used to return the
// first page of a broad vendor search over and over, so a walk that advances
// `offset` while `has_more` is true never terminates and never sees the rest
// of the registrants.
func TestUnknownArgumentIsRefusedByName(t *testing.T) {
	cases := []struct {
		tool  string
		args  string
		field string
	}{
		{ToolLookupMAC, `{"mac":"286FB9000001","verbose":true}`, "verbose"},
		{ToolLookupMAC, `{"mac_":"286FB9000001"}`, "mac_"},
		{ToolSearchVendor, `{"query":"Nokia","offest":50}`, "offest"},
		{ToolSearchVendor, `{"query":"Nokia","max":10}`, "max"},
		{ToolDBStatus, `{"verbose":true}`, "verbose"},
		{ToolUpdateDB, `{"force":true}`, "force"},
		{ToolGetUsage, `{"topic":"registries"}`, "topic"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.field, func(t *testing.T) {
			e, _ := newServer(t, true)
			text, isErr := callText(t, rpc(t, e, call(tc.tool, tc.args))[0])
			if !isErr {
				t.Fatalf("%s accepted unknown argument %q: %s", tc.tool, tc.field, text)
			}
			// Matching the decoder's own phrasing, not just the field name:
			// "provide 'hash…'" happens to contain "hash", so a bare substring
			// test passes for the wrong reason. The mutation check caught it.
			want := `unknown field "` + tc.field + `"`
			if !strings.Contains(text, want) {
				t.Errorf("%s: error does not name the offending argument: want %s, got %s", tc.tool, want, text)
			}
		})
	}
}

// TestMalformedArgumentsAreRefused covers the other half of the discarded
// error: `_ = json.Unmarshal` left `a` at its zero value when the object did
// not decode, so a wrong-typed argument produced the same call as an absent
// one — and "provide 'mac'" is a misleading answer to a request that did
// provide it.
func TestMalformedArgumentsAreRefused(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
	}{
		{"number for string", ToolLookupMAC, `{"mac":1}`},
		{"string for array", ToolLookupMAC, `{"macs":"286FB9000001"}`},
		{"array for object", ToolLookupMAC, `["286FB9000001"]`},
		{"string for integer", ToolSearchVendor, `{"query":"Nokia","limit":"all"}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.name, func(t *testing.T) {
			e, _ := newServer(t, true)
			text, isErr := callText(t, rpc(t, e, call(tc.tool, tc.args))[0])
			if !isErr {
				t.Fatalf("%s accepted malformed arguments: %s", tc.tool, text)
			}
			if strings.Contains(text, "provide '") {
				t.Errorf("%s reported the argument as missing instead of malformed: %s", tc.tool, text)
			}
			if !strings.Contains(text, "arguments:") {
				t.Errorf("%s: error is not a decode error: %s", tc.tool, text)
			}
		})
	}
}

// TestOmittedArgumentsStillMeanNone pins the boundary of the change: strict
// decoding must not turn a legitimately argument-less call into an error.
func TestOmittedArgumentsStillMeanNone(t *testing.T) {
	for _, args := range []string{`{}`, `null`} {
		e, _ := newServer(t, true)
		text, isErr := callText(t, rpc(t, e, call(ToolDBStatus, args))[0])
		if isErr {
			t.Errorf("db_status with arguments %q was refused: %s", args, text)
		}
	}
}
