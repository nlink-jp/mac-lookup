// Package config resolves mac-lookup settings from a sectioned TOML file plus
// environment overrides. It parses only the small TOML subset the tool needs,
// keeping the binary free of external dependencies.
//
// Like tor-exit-lookup and unlike asn-lookup (ipinfo token) or abuse-lookup
// (AbuseIPDB key), mac-lookup needs no credentials: the IEEE registry files are
// public. There is deliberately no credential field to configure, log, or leak.
//
// Scaffold: the settings surface and its defaults are fixed here; the TOML
// subset parser and Load land in Phase 3.
package config

import "time"

const (
	// DefaultTTL is how old the cached registry may be before an auto-refetch is
	// triggered on the next lookup. IEEE regenerates the files about once a day.
	DefaultTTL = 24 * time.Hour
	// MinTTL floors TTL out of fetch etiquette. The upstream files change at
	// most daily, so refetching more often than this only wastes IEEE's
	// bandwidth; an auto-refetch cannot fire faster regardless of configuration.
	MinTTL = 6 * time.Hour
	// StaleAfter is when `status` starts warning that the cache is old. A week
	// of drift is enough to miss a newly assigned prefix.
	StaleAfter = 7 * 24 * time.Hour
)

// EnvPrefix is the prefix for every environment override
// (MAC_LOOKUP_BASE_URL, MAC_LOOKUP_STORE, MAC_LOOKUP_TTL_MINUTES,
// MAC_LOOKUP_AUTO_UPDATE, MAC_LOOKUP_WORKSPACE).
const EnvPrefix = "MAC_LOOKUP_"

// Config holds resolved runtime settings.
type Config struct {
	BaseURL    string        // IEEE registry download origin
	StorePath  string        // path to the local cached registry store (JSON)
	Workspace  string        // default output directory for file-mediated MCP results
	TTL        time.Duration // auto-refetch threshold (floored at MinTTL)
	AutoUpdate bool          // auto-refetch on lookup when older than TTL
}
