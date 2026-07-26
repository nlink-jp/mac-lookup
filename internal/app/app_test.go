package app

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunMetaCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no args prints usage and fails", nil, exitError},
		{"help succeeds", []string{"help"}, 0},
		{"help flag succeeds", []string{"--help"}, 0},
		{"version succeeds", []string{"version"}, 0},
		{"version flag succeeds", []string{"--version"}, 0},
		{"unknown command fails", []string{"nope"}, exitError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Run(tt.args, "test"); got != tt.want {
				t.Errorf("Run(%q) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

// isolated returns flags that point every command at a scratch config and an
// absent store, so a test can exercise dispatch without touching the network or
// the developer's own cache.
func isolated(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return []string{
		"--config", filepath.Join(dir, "absent.toml"),
		"--store", filepath.Join(dir, "absent.json"),
	}
}

func TestRunDataCommandsWithoutCache(t *testing.T) {
	// With no registry downloaded, every read command must fail cleanly with
	// the "run update" hint rather than reaching for the network. --no-update
	// keeps `lookup` from auto-refetching during the test.
	tests := []struct {
		name string
		args []string
	}{
		{"lookup", []string{"lookup", "--no-update", "00:11:22:33:44:55"}},
		{"search", []string{"search", "Apple"}},
		{"status", []string{"status"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append(tt.args, isolated(t)...)
			if got := Run(args, "test"); got != exitError {
				t.Errorf("Run(%q) = %d, want %d", args, got, exitError)
			}
		})
	}
}

func TestRunLookupRejectsUnparseableAddress(t *testing.T) {
	args := append([]string{"lookup", "--no-update", "not-a-mac"}, isolated(t)...)
	// The address is rejected before the store is ever consulted.
	if got := Run(args, "test"); got != exitError {
		t.Errorf("Run(%q) = %d, want %d", args, got, exitError)
	}
}

func TestRunMCPExitsCleanlyOnEOF(t *testing.T) {
	// Serve returns at EOF; with no stdin attached the loop ends immediately.
	args := append([]string{"mcp"}, isolated(t)...)
	if got := Run(args, "test"); got != 0 {
		t.Errorf("Run(%q) = %d, want 0", args, got)
	}
}

func TestReadInputs(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
		want  []string
	}{
		{
			name: "args win over stdin",
			args: []string{"00:11:22:33:44:55"},
			// stdin must be ignored entirely when positional args are present.
			stdin: "aa:bb:cc:dd:ee:ff",
			want:  []string{"00:11:22:33:44:55"},
		},
		{
			name:  "stdin is split on whitespace",
			stdin: "00:11:22:33:44:55 aa-bb-cc-dd-ee-ff\n0011.2233.4455\n",
			want:  []string{"00:11:22:33:44:55", "aa-bb-cc-dd-ee-ff", "0011.2233.4455"},
		},
		{
			name:  "blank and comment lines are skipped",
			stdin: "\n# a capture note\n00:11:22\n\n",
			want:  []string{"00:11:22"},
		},
		{
			name:  "empty stdin yields nothing",
			stdin: "",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readInputs(tt.args, strings.NewReader(tt.stdin))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("readInputs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
