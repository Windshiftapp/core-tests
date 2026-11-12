//go:build test

package config

import (
	"embed"
	"flag"
	"os"
	"testing"
)

// TestLoadJiraImportWatchers is the WI-1983 configuration contract: watcher
// import is off unless an operator explicitly enables it.
func TestLoadJiraImportWatchers(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "unset disables watcher import", want: false},
		{name: "true enables watcher import", env: "true", want: true},
		{name: "1 enables watcher import", env: "1", want: true},
		{name: "false disables watcher import", env: "false", want: false},
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
			t.Setenv("JIRA_IMPORT_WATCHERS", tt.env)

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.Jira.ImportWatchers != tt.want {
				t.Fatalf("Jira.ImportWatchers = %t, want %t", cfg.Jira.ImportWatchers, tt.want)
			}
		})
	}
}
