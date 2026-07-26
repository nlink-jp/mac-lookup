package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nlink-jp/mac-lookup/internal/config"
	"github.com/nlink-jp/mac-lookup/internal/engine"
	"github.com/nlink-jp/mac-lookup/internal/ieee"
	"github.com/nlink-jp/mac-lookup/internal/macaddr"
	"github.com/nlink-jp/mac-lookup/internal/mcp"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// commonFlags are the config-resolution flags shared by every command.
type commonFlags struct {
	config  string
	store   string
	baseURL string
}

// register binds the common flags onto fs. When withURL is true it also
// registers --base-url (only meaningful for commands that download).
func (c *commonFlags) register(fs *flag.FlagSet, withURL bool) {
	fs.StringVar(&c.config, "config", "", "config file path")
	fs.StringVar(&c.config, "c", "", "config file path (shorthand)")
	fs.StringVar(&c.store, "store", "", "registry store path override")
	if withURL {
		fs.StringVar(&c.baseURL, "base-url", "", "IEEE registry origin override")
	}
}

func (c *commonFlags) buildEngine() (*engine.Engine, error) {
	cfg, err := config.Load(c.config, c.store, c.baseURL)
	if err != nil {
		return nil, err
	}
	return engine.New(cfg, ieee.NewHTTPFetcher()), nil
}

// loadDBOrHint loads the store, printing an actionable hint on ErrNoDB.
func loadDBOrHint(e *engine.Engine, errw io.Writer) (*ouidb.DB, int) {
	db, err := e.LoadDB()
	if err != nil {
		if errors.Is(err, engine.ErrNoDB) {
			fmt.Fprintf(errw, "%v\nrun 'mac-lookup update' to download the IEEE registries.\n", err)
			return nil, exitError
		}
		fmt.Fprintf(errw, "error: %v\n", err)
		return nil, exitError
	}
	return db, 0
}

// parseInterspersed parses fs while tolerating flags that appear after
// positional arguments (Go's flag package otherwise stops at the first
// non-flag). MAC inputs never begin with '-', so there is no ambiguity.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
	return positionals, nil
}

func cmdLookup(args []string) int {
	fs := flag.NewFlagSet("lookup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var common commonFlags
	common.register(fs, true)
	var asJSON, noUpdate bool
	fs.BoolVar(&asJSON, "json", false, "JSON Lines output")
	fs.BoolVar(&asJSON, "j", false, "JSON Lines output (shorthand)")
	fs.BoolVar(&noUpdate, "no-update", false, "never auto-refetch a stale registry")

	positionals, err := parseInterspersed(fs, args)
	if err != nil {
		return exitError
	}
	inputs := readInputs(positionals, os.Stdin)
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "lookup: no address given")
		return exitError
	}

	e, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}

	db, code := resolveDB(e, noUpdate, os.Stderr)
	if db == nil {
		return code
	}

	// The grep-style tri-state is only meaningful for a single address in text
	// mode. A batch reports errors only, so one unresolvable address in a
	// thousand does not look like a failed run.
	single := len(inputs) == 1 && len(positionals) == 1 && !asJSON
	failed := false
	var lastCode int
	for _, in := range inputs {
		r, rerr := engine.Resolve(db, in)
		if rerr != nil {
			failed = true
			if asJSON {
				_ = writeJSONLine(os.Stdout, lookupJSON{Input: in, Error: "invalid address"})
			} else {
				fmt.Fprintf(os.Stderr, "%s: %v\n", in, rerr)
			}
			lastCode = exitError
			continue
		}
		if asJSON {
			if werr := writeJSONLine(os.Stdout, toJSON(r)); werr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", werr)
				return exitError
			}
		} else {
			writeText(os.Stdout, r)
		}
		if r.VendorName() == "" {
			lastCode = exitNoVendor
		} else {
			lastCode = exitVendorFound
		}
	}

	if single {
		return lastCode
	}
	if failed {
		return exitError
	}
	return exitVendorFound
}

// resolveDB loads the registry, auto-refetching when it is stale and allowed.
// A refresh failure degrades to the cached copy with a warning rather than
// failing the lookup: an offline answer from last week beats no answer.
func resolveDB(e *engine.Engine, noUpdate bool, errw io.Writer) (*ouidb.DB, int) {
	if noUpdate || !e.Cfg.AutoUpdate {
		return loadDBOrHint(e, errw)
	}
	db, _, err := e.EnsureFresh(context.Background(), e.Cfg.TTL)
	if err != nil {
		if db == nil {
			if errors.Is(err, engine.ErrNoDB) {
				fmt.Fprintf(errw, "%v\nrun 'mac-lookup update' to download the IEEE registries.\n", err)
			} else {
				fmt.Fprintf(errw, "error: %v\n", err)
			}
			return nil, exitError
		}
		fmt.Fprintf(errw, "warning: could not refresh the registry (%v); using the cached copy\n", err)
	}
	return db, 0
}

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var common commonFlags
	common.register(fs, false)
	var asJSON bool
	var limit int
	fs.BoolVar(&asJSON, "json", false, "JSON Lines output")
	fs.BoolVar(&asJSON, "j", false, "JSON Lines output (shorthand)")
	fs.IntVar(&limit, "limit", 200, "maximum rows to print (0 = no limit)")

	positionals, err := parseInterspersed(fs, args)
	if err != nil {
		return exitError
	}
	query := strings.TrimSpace(strings.Join(positionals, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "search: no vendor name given")
		return exitError
	}

	e, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	db, code := loadDBOrHint(e, os.Stderr)
	if db == nil {
		return code
	}

	matches, total := engine.SearchVendor(db, query, limit)
	for _, m := range matches {
		if asJSON {
			if werr := writeJSONLine(os.Stdout, toSearchJSON(m)); werr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", werr)
				return exitError
			}
			continue
		}
		fmt.Printf("%-18s %-6s /%-2d  %s\n", macaddr.Canonical(m.Assignment), m.Registry, m.PrefixBits, m.Organization)
	}
	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "no registrant matches %q\n", query)
		return exitNoVendor
	}
	// Never let a truncated list read as the whole answer.
	if total > len(matches) {
		fmt.Fprintf(os.Stderr, "showing %d of %d matches; raise --limit to see the rest\n", len(matches), total)
	}
	return exitVendorFound
}

func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var common commonFlags
	common.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitError
	}

	e, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	res, err := e.Update(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		return exitError
	}

	fmt.Printf("Updated %s\n", res.StorePath)
	fmt.Printf("  generated: %s\n", res.Generated.Format(time.RFC3339))
	fmt.Printf("  assignments: %d\n", res.Total)
	for _, reg := range engine.SortedRegistries(res.Counts) {
		fmt.Printf("    %-5s %d\n", reg, res.Counts[reg])
	}
	if len(res.NotModified) > 0 {
		fmt.Printf("  unchanged upstream: %s\n", joinRegistries(res.NotModified))
	}
	if res.Skipped > 0 {
		fmt.Printf("  skipped rows: %d\n", res.Skipped)
	}
	// Warnings go to stderr: a degraded update still exits 0, so the operator
	// must be able to see the degradation without parsing stdout.
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	return exitVendorFound
}

func joinRegistries(regs []ouidb.Registry) string {
	parts := make([]string, len(regs))
	for i, r := range regs {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var common commonFlags
	common.register(fs, false)
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "JSON output")
	fs.BoolVar(&asJSON, "j", false, "JSON output (shorthand)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}

	e, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	db, code := loadDBOrHint(e, os.Stderr)
	if db == nil {
		return code
	}

	counts := db.CountsByRegistry()
	stale, age := e.IsStale(db.Generated())
	if asJSON {
		regs := map[string]int{}
		for r, n := range counts {
			regs[string(r)] = n
		}
		if werr := writeJSONLine(os.Stdout, map[string]any{
			"path":        e.Cfg.StorePath,
			"generated":   db.Generated(),
			"assignments": db.Len(),
			"registries":  regs,
			"sources":     db.Sources,
			"stale":       stale,
			"age_hours":   int(age.Hours()),
		}); werr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", werr)
			return exitError
		}
		return exitVendorFound
	}

	fmt.Printf("Store:       %s\n", e.Cfg.StorePath)
	fmt.Printf("Generated:   %s (%d hours ago)\n", db.Generated().Format(time.RFC3339), int(age.Hours()))
	fmt.Printf("Assignments: %d\n", db.Len())
	for _, reg := range engine.SortedRegistries(counts) {
		fmt.Printf("  %-5s %d\n", reg, counts[reg])
	}
	if len(db.Sources) < len(ieee.RegistryFiles) {
		fmt.Printf("Registries:  %d of %d cached — some lookups will be less precise\n", len(db.Sources), len(ieee.RegistryFiles))
	}
	if stale {
		fmt.Printf("Stale:       yes — run 'mac-lookup update'\n")
	}
	return exitVendorFound
}

func cmdMCP(args []string, version string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var common commonFlags
	common.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	e, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	// stdout carries the JSON-RPC stream; diagnostics must go to stderr only.
	if err := mcp.Serve(context.Background(), e, version, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		return exitError
	}
	return exitVendorFound
}
