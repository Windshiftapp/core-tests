//go:build test

package config

import (
	"embed"
	"flag"
	"os"
	"testing"
)

// TestLoadJiraImportConcurrency is the WI-1982 configuration contract: the
// import pool uses the built-in default unless an operator overrides it.
func TestLoadJiraImportConcurrency(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "unset uses the built-in default", want: 0},
		{name: "explicit value is read from the environment", env: "4", want: 4},
		{name: "unparseable value keeps the default", env: "many", want: 0},
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
			t.Setenv("JIRA_IMPORT_CONCURRENCY", tt.env)

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.Jira.ImportConcurrency != tt.want {
				t.Fatalf("Jira.ImportConcurrency = %d, want %d", cfg.Jira.ImportConcurrency, tt.want)
			}
		})
	}
}
