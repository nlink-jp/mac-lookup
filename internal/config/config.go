// Package config resolves mac-lookup settings from a sectioned TOML file plus
// environment overrides. It parses only the small TOML subset the tool needs,
// keeping the binary free of external dependencies.
//
// Like tor-exit-lookup and unlike asn-lookup (ipinfo token) or abuse-lookup
// (AbuseIPDB key), mac-lookup needs no credentials: the IEEE registry files are
// public. There is deliberately no credential field to configure, log, or leak.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nlink-jp/mac-lookup/internal/ieee"
)

const (
	// DefaultTTL is how old the cached registry may be before an auto-refetch is
	// triggered on the next lookup. IEEE regenerates the files about once a day.
	DefaultTTL = 24 * time.Hour
	// MinTTL floors TTL out of fetch etiquette. The upstream files change at
	// most daily, so refetching more often than this only wastes IEEE's
	// bandwidth; an auto-refetch cannot fire faster regardless of configuration.
	MinTTL = 6 * time.Hour
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

// Load resolves configuration. If configPath is empty the default location
// (~/.config/mac-lookup/config.toml) is used when present. Environment
// variables override file values, and any explicit non-empty override argument
// wins over both.
func Load(configPath, storeOverride, baseURLOverride string) (*Config, error) {
	cfg := &Config{
		BaseURL:    ieee.DefaultBaseURL,
		StorePath:  DefaultStorePath(),
		Workspace:  DefaultWorkspaceDir(),
		TTL:        DefaultTTL,
		AutoUpdate: true,
	}

	if configPath == "" {
		configPath = DefaultConfigPath()
	}
	if configPath != "" {
		if f, err := os.Open(configPath); err == nil {
			defer f.Close()
			sections, perr := parseTOML(f)
			if perr != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, perr)
			}
			if aerr := applySections(cfg, sections); aerr != nil {
				return nil, fmt.Errorf("config %s: %w", configPath, aerr)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("open config %s: %w", configPath, err)
		}
	}

	// Environment overrides.
	if v := os.Getenv(EnvPrefix + "BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv(EnvPrefix + "STORE"); v != "" {
		cfg.StorePath = expandHome(v)
	}
	if v := os.Getenv(EnvPrefix + "WORKSPACE"); v != "" {
		cfg.Workspace = expandHome(v)
	}
	if v := os.Getenv(EnvPrefix + "TTL_MINUTES"); v != "" {
		d, err := parseTTLMinutes(v)
		if err != nil {
			return nil, fmt.Errorf("%sTTL_MINUTES: %w", EnvPrefix, err)
		}
		cfg.TTL = d
	}
	if v := os.Getenv(EnvPrefix + "AUTO_UPDATE"); v != "" {
		if b, ok := parseBool(v); ok {
			cfg.AutoUpdate = b
		}
	}

	// Explicit flag overrides win.
	if baseURLOverride != "" {
		cfg.BaseURL = baseURLOverride
	}
	if storeOverride != "" {
		cfg.StorePath = expandHome(storeOverride)
	}

	// Enforce the polling-etiquette floor on TTL.
	if cfg.TTL < MinTTL {
		cfg.TTL = MinTTL
	}

	return cfg, nil
}

func applySections(cfg *Config, sections map[string]map[string]string) error {
	if i := sections["ieee"]; i != nil {
		if v := i["base_url"]; v != "" {
			cfg.BaseURL = v
		}
		if v := i["ttl_minutes"]; v != "" {
			d, err := parseTTLMinutes(v)
			if err != nil {
				return fmt.Errorf("[ieee] ttl_minutes: %w", err)
			}
			cfg.TTL = d
		}
		if v := i["auto_update"]; v != "" {
			if b, ok := parseBool(v); ok {
				cfg.AutoUpdate = b
			}
		}
	}
	if s := sections["store"]; s != nil {
		if v := s["path"]; v != "" {
			cfg.StorePath = expandHome(v)
		}
	}
	if w := sections["workspace"]; w != nil {
		if v := w["path"]; v != "" {
			cfg.Workspace = expandHome(v)
		}
	}
	return nil
}

// parseTTLMinutes parses a non-negative minutes value into a Duration.
func parseTTLMinutes(v string) (time.Duration, error) {
	m, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	if m < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return time.Duration(m * float64(time.Minute)), nil
}

// parseBool accepts the common truthy/falsey spellings.
func parseBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// DefaultConfigPath returns the default config file location, honoring
// XDG_CONFIG_HOME.
func DefaultConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "mac-lookup", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mac-lookup", "config.toml")
}

// DefaultStorePath returns the default registry store location, honoring
// XDG_DATA_HOME.
func DefaultStorePath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "mac-lookup", "ouidb.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "ouidb.json"
	}
	return filepath.Join(home, ".local", "share", "mac-lookup", "ouidb.json")
}

// DefaultWorkspaceDir returns the default MCP output directory, honoring
// XDG_STATE_HOME (file-mediated results are reproducible, transient state).
func DefaultWorkspaceDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "mac-lookup", "workspace")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "mac-lookup", "workspace")
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// parseTOML parses the minimal subset mac-lookup needs: [section] headers and
// key = value lines, where value is an optionally quoted string. Comments start
// with '#'. It intentionally does not support arrays, nested tables, or typed
// values.
func parseTOML(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	current := "" // top-level keys land in the "" section
	sections[current] = map[string]string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "[") {
			end := strings.IndexByte(raw, ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated section header", line)
			}
			current = strings.TrimSpace(raw[1:end])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", line)
		}
		key := strings.TrimSpace(raw[:eq])
		val := parseValue(strings.TrimSpace(raw[eq+1:]))
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", line)
		}
		sections[current][key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseValue strips surrounding quotes, or trims a trailing inline comment from
// a bare value.
func parseValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		q := v[0]
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
	}
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = strings.TrimSpace(v[:hash])
	}
	return v
}
