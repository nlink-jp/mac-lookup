package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
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
		// Scaffold: the data commands are recognised by the dispatcher but not
		// implemented until Phase 3. These cases change to real assertions then.
		{"lookup is dispatched", []string{"lookup", "00:11:22:33:44:55"}, exitError},
		{"search is dispatched", []string{"search", "Apple"}, exitError},
		{"update is dispatched", []string{"update"}, exitError},
		{"status is dispatched", []string{"status"}, exitError},
		{"mcp is dispatched", []string{"mcp"}, exitError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Run(tt.args, "test"); got != tt.want {
				t.Errorf("Run(%q) = %d, want %d", tt.args, got, tt.want)
			}
		})
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
