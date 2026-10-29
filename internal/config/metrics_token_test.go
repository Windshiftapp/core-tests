//go:build test

package config

import (
	"embed"
	"flag"
	"os"
	"testing"
)

func TestLoadMetricsToken(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "unset leaves metrics public", want: ""},
		{name: "token is read from environment", env: "metrics-scrape-token", want: "metrics-scrape-token"},
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
			t.Setenv("WINDSHIFT_METRICS_TOKEN", tt.env)

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.MetricsToken != tt.want {
				t.Fatalf("MetricsToken = %q, want %q", cfg.MetricsToken, tt.want)
			}
		})
	}
}
