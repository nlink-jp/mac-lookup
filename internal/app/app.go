// Package app implements the mac-lookup command-line interface: subcommand
// dispatch plus the lookup / search / update / status / mcp commands. Core
// logic lives in the macaddr, ouidb, ieee, config, engine and mcp packages;
// this package is the thin I/O shell around them.
//
// Scaffold: dispatch, help and version are live so the build chain is
// exercised end to end. The data commands are wired to a placeholder until
// Phase 3 implements the engine behind them.
package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Exit codes. `lookup` uses the grep-style convention so it composes in shell:
//
//	if mac-lookup lookup "$mac"; then echo "vendor known"; fi
//
// The tri-state applies to a single positional address in text mode only.
// Multiple addresses, stdin, or --json switch to batch mode, where the exit
// code reports errors only (0/2) and the results go to stdout.
const (
	exitVendorFound = 0 // the address resolved to a named vendor
	exitNoVendor    = 1 // unassigned, locally administered, multicast, or Private
	exitError       = 2 // usage / lookup error
)

// Run dispatches a subcommand and returns a process exit code.
func Run(args []string, version string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return exitError
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "lookup":
		return cmdLookup(rest)
	case "search":
		return cmdSearch(rest)
	case "update":
		return cmdUpdate(rest)
	case "status":
		return cmdStatus(rest)
	case "mcp":
		return cmdMCP(rest, version)
	case "version", "--version", "-v":
		fmt.Println("mac-lookup " + version)
		fmt.Println("Data: IEEE Registration Authority public registries (https://standards.ieee.org/products-programs/regauth/).")
		return 0
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return exitError
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `mac-lookup — what made this MAC, and what kind of address is it? (offline)

Usage:
  mac-lookup <command> [flags] [args]

Commands:
  lookup <MAC>...   Resolve MAC addresses, BSSIDs or prefixes (stdin if no args)
  search <query>    Find a vendor's assigned prefixes by name
  update            Download the IEEE registries and rebuild the local store
  status            Show the cached registry's freshness and size
  mcp               Run as a local MCP server (stdio)
  version           Print the version

lookup flags:
  -j, --json        JSON Lines output (one object per address)
  --no-update       Do not auto-refetch even if the registry is stale

lookup exit codes (single address, text mode):
  0  resolved to a named vendor
  1  no vendor name (unassigned, locally administered, multicast, or Private)
  2  error (unparseable address, no local registry, ...)
  (batch mode — multiple addresses, stdin, or --json — exits 0 unless an error occurs)

Accepted address forms:
  00:11:22:33:44:55   00-11-22-33-44-55   001122334455   0011.2233.4455
  and bare prefixes at 24, 28 or 36 bits (00:11:22, 8C:1F:64:AF:A)

Common flags:
  -c, --config <path>   Config file (default ~/.config/mac-lookup/config.toml)
  --store <path>        Registry store (default ~/.local/share/mac-lookup/ouidb.json)

The registry auto-refetches when older than the TTL (default 24h, 6h floor);
disable with --no-update or [ieee] auto_update = false.

Data: IEEE Registration Authority public registries
(https://standards.ieee.org/products-programs/regauth/).
`)
}

// readInputs returns args verbatim, or whitespace-separated tokens read from
// stdin when args is empty. Blank lines and '#' comment lines are skipped.
func readInputs(args []string, stdin io.Reader) []string {
	if len(args) > 0 {
		return args
	}
	var out []string
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line)...)
	}
	return out
}
