//go:build test

package config

import (
	"embed"
	"flag"
	"os"
	"testing"
	"time"
)

// TestLoadJiraImportMaxDuration is the WI-1981 configuration contract: the
// overall Jira import limit is off unless an operator sets it.
func TestLoadJiraImportMaxDuration(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset disables the overall limit", want: 0},
		{name: "explicit zero disables the overall limit", env: "0s", want: 0},
		{name: "duration is read from the environment", env: "90m", want: 90 * time.Minute},
		{name: "unparseable value keeps the default", env: "not-a-duration", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previousCommandLine := flag.CommandLine
			previousArgs := os.Args
			t.Cleanup(func() {
				flag.CommandLine = previousCommandLine
				os.Args = previousArgs
			})

			flag.CommandLine = flag.NewFlagSet("windshift-test", flag.ContinueOnError)
			os.Args = []string{"windshift-test"}
			t.Setenv("SSO_SECRET", "test-session-secret")
			t.Setenv("WINDSHIFT_MEMORY_LIMIT_MB", "")
			t.Setenv("JIRA_IMPORT_MAX_DURATION", tt.env)

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.Jira.ImportMaxDuration != tt.want {
				t.Fatalf("Jira.ImportMaxDuration = %v, want %v", cfg.Jira.ImportMaxDuration, tt.want)
			}
		})
	}
}
