// Command mac-lookup resolves a MAC address or BSSID to its manufacturer and,
// more importantly, to the nature of the address itself (broadcast, multicast,
// locally administered), answered offline from a locally cached copy of the
// IEEE Registration Authority public registries, as a CLI and a local MCP
// server. The L2 sibling of asn-lookup (L3 attribution) and tor-exit-lookup
// (offline membership).
package main

import (
	"os"

	"github.com/nlink-jp/mac-lookup/internal/app"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
