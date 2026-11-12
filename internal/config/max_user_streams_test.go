//go:build test

package config

import (
	"embed"
	"flag"
	"os"
	"testing"
)

func TestLoadMaxUserStreams(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want int
	}{
		{name: "default leaves room for several tabs", want: 32},
		{name: "explicit flag", args: []string{"--max-user-streams=12"}, want: 12},
		{name: "environment overrides the flag", args: []string{"--max-user-streams=12"}, env: "5", want: 5},
		{name: "zero disables the cap", env: "0", want: 0},
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
			t.Setenv("MAX_USER_STREAMS", tt.env)

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.MaxUserStreams != tt.want {
				t.Fatalf("MaxUserStreams = %d, want %d", cfg.MaxUserStreams, tt.want)
			}
		})
	}
}
