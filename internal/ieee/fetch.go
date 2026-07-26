// Package ieee fetches the IEEE Registration Authority public registry files.
//
// The files are public and require no authentication, so there is no secret to
// redact. Two constraints shape this package, both measured against the live
// service on 2026-07-26:
//
//   - The origin rejects spoofed browser User-Agents with 418 I'm a Teapot,
//     while a plain client User-Agent is served normally. Do not pretend to be
//     a browser; identify the tool honestly.
//   - Every file carries ETag and Last-Modified, so a conditional GET turns a
//     no-op refresh into a 304 instead of a 5.6 MB download.
package ieee

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// UserAgent identifies this client to the IEEE servers. Keep it honest: a
// browser-shaped User-Agent is answered with HTTP 418.
const UserAgent = "mac-lookup (+https://github.com/nlink-jp/mac-lookup)"

// DefaultBaseURL is the origin serving the registry downloads.
const DefaultBaseURL = "https://standards-oui.ieee.org"

// RegistryFile locates one registry CSV relative to the base URL.
type RegistryFile struct {
	Registry ouidb.Registry
	Path     string
}

// RegistryFiles is the set of registries mac-lookup consumes. The EtherType,
// Manufacturer ID and Operator ID files IEEE also publishes are deliberately
// absent: they play no part in resolving a MAC address.
var RegistryFiles = []RegistryFile{
	{Registry: ouidb.RegistryMAL, Path: "/oui/oui.csv"},
	{Registry: ouidb.RegistryMAM, Path: "/oui28/mam.csv"},
	{Registry: ouidb.RegistryMAS, Path: "/oui36/oui36.csv"},
	{Registry: ouidb.RegistryIAB, Path: "/iab/iab.csv"},
	{Registry: ouidb.RegistryCID, Path: "/cid/cid.csv"},
}

// URLFor joins a base URL and a registry path.
func URLFor(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

// Conditional carries the validators from a previous fetch so the server can
// answer 304 instead of resending an unchanged file.
type Conditional struct {
	ETag         string
	LastModified string
}

// Response is the outcome of a fetch. Body is nil when NotModified is set, and
// otherwise must be closed by the caller.
type Response struct {
	NotModified  bool
	Body         io.ReadCloser
	ETag         string
	LastModified string
}

// Fetcher retrieves a registry file. It is an interface so the engine can be
// tested without touching the network.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string, cond Conditional) (*Response, error)
}

// HTTPFetcher is the production Fetcher.
type HTTPFetcher struct {
	Client *http.Client
}

// NewHTTPFetcher returns a Fetcher with a sane default timeout. The largest
// registry file is a few megabytes, so a couple of minutes is generous.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{Client: &http.Client{Timeout: 2 * time.Minute}}
}

// Fetch performs a conditional GET.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string, cond Conditional) (*Response, error) {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if cond.ETag != "" {
		req.Header.Set("If-None-Match", cond.ETag)
	}
	if cond.LastModified != "" {
		req.Header.Set("If-Modified-Since", cond.LastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	switch resp.StatusCode {
	case http.StatusNotModified:
		resp.Body.Close()
		return &Response{NotModified: true, ETag: cond.ETag, LastModified: cond.LastModified}, nil
	case http.StatusOK:
		return &Response{
			Body:         resp.Body,
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
		}, nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	resp.Body.Close()
	if resp.StatusCode == http.StatusTeapot {
		// The origin answers browser-shaped User-Agents this way. Seeing it here
		// means something rewrote the request header, so say so plainly rather
		// than leaving a bare "418" to be puzzled over.
		return nil, fmt.Errorf("download %s: HTTP 418 — the IEEE origin rejects browser-like User-Agents; send %q instead", rawURL, UserAgent)
	}
	return nil, fmt.Errorf("download %s: HTTP %d: %s", rawURL, resp.StatusCode, trimBody(body))
}

func trimBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
