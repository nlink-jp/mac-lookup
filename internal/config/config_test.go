package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func hasField(v any, name string) bool {
	_, ok := reflect.TypeOf(v).Elem().FieldByName(name)
	return ok
}

// isolate points every XDG lookup at a temp dir so the developer's real
// configuration cannot influence the test.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return dir
}

func TestLoadDefaults(t *testing.T) {
	isolate(t)
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.BaseURL == "" || !strings.HasPrefix(cfg.BaseURL, "https://") {
		t.Errorf("BaseURL = %q, want an https origin", cfg.BaseURL)
	}
	if cfg.TTL != DefaultTTL {
		t.Errorf("TTL = %v, want %v", cfg.TTL, DefaultTTL)
	}
	if !cfg.AutoUpdate {
		t.Error("AutoUpdate = false, want true by default")
	}
	if !strings.HasSuffix(cfg.StorePath, filepath.Join("mac-lookup", "ouidb.json")) {
		t.Errorf("StorePath = %q", cfg.StorePath)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "config.toml")
	content := `
# a comment
[ieee]
base_url = "https://example.invalid/registry"
ttl_minutes = 2880
auto_update = false

[store]
path = "/var/lib/mac-lookup/db.json"

[workspace]
path = "/var/tmp/mac-lookup"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "", "")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.BaseURL != "https://example.invalid/registry" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.TTL != 48*time.Hour {
		t.Errorf("TTL = %v, want 48h", cfg.TTL)
	}
	if cfg.AutoUpdate {
		t.Error("AutoUpdate = true, want false from the file")
	}
	if cfg.StorePath != "/var/lib/mac-lookup/db.json" {
		t.Errorf("StorePath = %q", cfg.StorePath)
	}
	if cfg.Workspace != "/var/tmp/mac-lookup" {
		t.Errorf("Workspace = %q", cfg.Workspace)
	}
}

func TestPrecedence(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[ieee]\nbase_url = \"https://from-file.invalid\"\n[store]\npath = \"/from/file.json\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPrefix+"BASE_URL", "https://from-env.invalid")
	t.Setenv(EnvPrefix+"STORE", "/from/env.json")

	// Environment beats the file.
	cfg, err := Load(path, "", "")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.BaseURL != "https://from-env.invalid" || cfg.StorePath != "/from/env.json" {
		t.Errorf("environment did not override the file: %q %q", cfg.BaseURL, cfg.StorePath)
	}

	// An explicit flag beats both.
	cfg, err = Load(path, "/from/flag.json", "https://from-flag.invalid")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.BaseURL != "https://from-flag.invalid" || cfg.StorePath != "/from/flag.json" {
		t.Errorf("flags did not win: %q %q", cfg.BaseURL, cfg.StorePath)
	}
}

func TestTTLFloorIsEnforced(t *testing.T) {
	isolate(t)
	// IEEE regenerates the files about once a day. A caller asking to poll every
	// minute is clamped rather than obeyed, from either source.
	t.Setenv(EnvPrefix+"TTL_MINUTES", "1")
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.TTL != MinTTL {
		t.Errorf("TTL = %v, want the %v floor", cfg.TTL, MinTTL)
	}
}

func TestInvalidTTLIsAnError(t *testing.T) {
	isolate(t)
	t.Setenv(EnvPrefix+"TTL_MINUTES", "soon")
	if _, err := Load("", "", ""); err == nil {
		t.Error("Load accepted a non-numeric TTL")
	}
	t.Setenv(EnvPrefix+"TTL_MINUTES", "-5")
	if _, err := Load("", "", ""); err == nil {
		t.Error("Load accepted a negative TTL")
	}
}

func TestMissingConfigFileIsNotAnError(t *testing.T) {
	dir := isolate(t)
	// The config file is optional; its absence must not stop the tool.
	if _, err := Load(filepath.Join(dir, "absent.toml"), "", ""); err != nil {
		t.Errorf("Load returned error for a missing config: %v", err)
	}
}

func TestMalformedConfigIsAnError(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("[ieee\nbase_url = x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "", ""); err == nil {
		t.Error("Load accepted an unterminated section header")
	}
}

func TestParseTOMLSubset(t *testing.T) {
	in := `
top = "level"
[ieee]
base_url = "https://x.invalid"  # trailing comment inside quotes is kept out
bare = value # trimmed
'single' = 'quoted'
`
	sections, err := parseTOML(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseTOML returned error: %v", err)
	}
	if sections[""]["top"] != "level" {
		t.Errorf("top-level key = %q", sections[""]["top"])
	}
	if got := sections["ieee"]["base_url"]; got != "https://x.invalid" {
		t.Errorf("quoted value = %q", got)
	}
	if got := sections["ieee"]["bare"]; got != "value" {
		t.Errorf("bare value with inline comment = %q", got)
	}
	if got := sections["ieee"]["'single'"]; got != "quoted" {
		t.Errorf("single-quoted value = %q", got)
	}
}

func TestParseBool(t *testing.T) {
	for _, s := range []string{"1", "true", "TRUE", "yes", "on"} {
		if b, ok := parseBool(s); !ok || !b {
			t.Errorf("parseBool(%q) = %v, %v", s, b, ok)
		}
	}
	for _, s := range []string{"0", "false", "no", "off"} {
		if b, ok := parseBool(s); !ok || b {
			t.Errorf("parseBool(%q) = %v, %v", s, b, ok)
		}
	}
	if _, ok := parseBool("maybe"); ok {
		t.Error("parseBool accepted an unrecognised spelling")
	}
}

func TestNoCredentialFieldExists(t *testing.T) {
	// The absence of a credential setting is a design guarantee, not an
	// oversight: the IEEE files are public. If someone adds a token field, this
	// test should be the thing that makes them justify it.
	isolate(t)
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	for _, field := range []string{"Token", "APIKey", "Key", "Secret", "Password"} {
		if hasField(cfg, field) {
			t.Errorf("Config gained a credential field %q", field)
		}
	}
}
