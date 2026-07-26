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
//
// Scaffold: the registry catalogue and the Fetcher contract are fixed here; the
// HTTP implementation lands in Phase 3.
package ieee

import (
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
