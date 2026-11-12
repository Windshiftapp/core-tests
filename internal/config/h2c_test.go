//go:build test

package config

import (
	"embed"
	"flag"
	"os"
	"testing"
)

func TestLoadH2CEnabled(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{name: "off by default", want: false},
		{name: "flag enables h2c", args: []string{"--h2c"}, want: true},
		{name: "environment enables h2c", env: "1", want: true},
		{name: "environment true enables h2c", env: "true", want: true},
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
			t.Setenv("WINDSHIFT_MEMORY_LIMIT_MB", "")
			t.Setenv("H2C_ENABLED", tt.env)

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.H2CEnabled != tt.want {
				t.Fatalf("H2CEnabled = %v, want %v", cfg.H2CEnabled, tt.want)
			}
		})
	}
}
