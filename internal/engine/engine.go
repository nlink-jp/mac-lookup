// Package engine ties config, the IEEE fetcher and the local store together.
// The CLI and the MCP server both go through it, so their behaviour cannot
// drift apart.
//
// Scaffold: the error surface is fixed here; LoadDB / Update / Lookup /
// SearchVendor / EnsureFresh / IsStale land in Phase 3.
package engine

import "errors"

// ErrNoDB is returned when no registry store exists yet. Callers turn it into
// an actionable hint rather than a bare failure.
var ErrNoDB = errors.New("no local IEEE registry cache")
