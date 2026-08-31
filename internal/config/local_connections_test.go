package config

import (
	"embed"
	"flag"
	"os"
	"testing"
)

func TestLoadAllowLocalConnections(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{name: "allowed by default", want: true},
		{name: "explicitly disallowed by CLI", args: []string{"--allow-local-connections=false"}, want: false},
		{name: "explicitly disallowed by environment", env: "false", want: false},
		{name: "environment overrides CLI", args: []string{"--allow-local-connections=false"}, env: "true", want: true},
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
			os.Args = append([]string{"windshift-test"}, tt.args...)
			t.Setenv("SSO_SECRET", "test-session-secret")
			t.Setenv("ALLOW_LOCAL_CONNECTIONS", tt.env)
			t.Setenv("WINDSHIFT_MEMORY_LIMIT_MB", "")

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.AllowLocalConnections != tt.want {
				t.Fatalf("AllowLocalConnections = %v, want %v", cfg.AllowLocalConnections, tt.want)
			}
		})
	}
}
