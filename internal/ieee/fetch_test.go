package ieee

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURLFor(t *testing.T) {
	tests := []struct {
		base, path, want string
	}{
		{"https://standards-oui.ieee.org", "/oui/oui.csv", "https://standards-oui.ieee.org/oui/oui.csv"},
		{"https://standards-oui.ieee.org/", "/oui/oui.csv", "https://standards-oui.ieee.org/oui/oui.csv"},
	}
	for _, tt := range tests {
		if got := URLFor(tt.base, tt.path); got != tt.want {
			t.Errorf("URLFor(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
		}
	}
}

func TestRegistryFilesCoverEveryLookupRegistry(t *testing.T) {
	// Five files, no more: the EtherType / manid / opid registries IEEE also
	// publishes play no part in resolving a MAC address.
	if len(RegistryFiles) != 5 {
		t.Fatalf("RegistryFiles has %d entries, want 5", len(RegistryFiles))
	}
	seen := map[string]bool{}
	for _, rf := range RegistryFiles {
		if seen[string(rf.Registry)] {
			t.Errorf("duplicate registry %q", rf.Registry)
		}
		seen[string(rf.Registry)] = true
	}
}

func TestFetchSendsHonestUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Sun, 26 Jul 2026 00:01:23 GMT")
		io.WriteString(w, "Registry,Assignment,Organization Name,Organization Address\n")
	}))
	defer srv.Close()

	resp, err := (&HTTPFetcher{}).Fetch(context.Background(), srv.URL, Conditional{})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	defer resp.Body.Close()

	if gotUA != UserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, UserAgent)
	}
	// Disguising the client as a browser is what gets a 418 from the real
	// origin, so the UA must not look like one.
	if strings.Contains(strings.ToLower(gotUA), "mozilla") {
		t.Error("User-Agent impersonates a browser")
	}
	if resp.ETag != `"abc"` || resp.LastModified == "" {
		t.Errorf("validators not captured: etag=%q last-modified=%q", resp.ETag, resp.LastModified)
	}
}

func TestFetchConditional(t *testing.T) {
	var gotINM, gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	cond := Conditional{ETag: `"abc"`, LastModified: "Sun, 26 Jul 2026 00:01:23 GMT"}
	resp, err := (&HTTPFetcher{}).Fetch(context.Background(), srv.URL, cond)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if gotINM != cond.ETag || gotIMS != cond.LastModified {
		t.Errorf("validators not sent: if-none-match=%q if-modified-since=%q", gotINM, gotIMS)
	}
	if !resp.NotModified {
		t.Error("NotModified = false, want true for a 304")
	}
	if resp.Body != nil {
		t.Error("Body is non-nil on a 304")
	}
	// The validators must survive a 304 so the next refresh can send them again.
	if resp.ETag != cond.ETag || resp.LastModified != cond.LastModified {
		t.Error("validators not carried through the 304")
	}
}

func TestFetchTeapotExplainsItself(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	_, err := (&HTTPFetcher{}).Fetch(context.Background(), srv.URL, Conditional{})
	if err == nil {
		t.Fatal("Fetch accepted a 418")
	}
	// A bare "HTTP 418" is a puzzle; the message must name the cause.
	if !strings.Contains(err.Error(), "User-Agent") {
		t.Errorf("error does not explain the 418: %v", err)
	}
}

func TestFetchOtherErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "no such registry")
	}))
	defer srv.Close()

	_, err := (&HTTPFetcher{}).Fetch(context.Background(), srv.URL, Conditional{})
	if err == nil {
		t.Fatal("Fetch accepted a 404")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "no such registry") {
		t.Errorf("error lacks status or body: %v", err)
	}
}
