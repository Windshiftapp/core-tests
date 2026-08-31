package config

import (
	"embed"
	"flag"
	"os"
	"testing"
)

func TestLoadTLSSkipVerify(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "unset keeps certificate verification enabled", want: false},
		{name: "false keeps certificate verification enabled", env: "false", want: false},
		{name: "true disables certificate verification", env: "true", want: true},
		{name: "one disables certificate verification", env: "1", want: true},
		{name: "yes disables certificate verification", env: "yes", want: true},
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
			t.Setenv("TLS_SKIP_VERIFY", tt.env)
			t.Setenv("WINDSHIFT_MEMORY_LIMIT_MB", "")

			cfg := Load(embed.FS{}, make(chan os.Signal, 1))
			if cfg.OutboundTLS.SkipVerify != tt.want {
				t.Fatalf("OutboundTLS.SkipVerify = %v, want %v", cfg.OutboundTLS.SkipVerify, tt.want)
			}
			logbookCfg := LoadLogbookSidecar()
			if logbookCfg.OutboundTLS.SkipVerify != tt.want {
				t.Fatalf("logbook OutboundTLS.SkipVerify = %v, want %v", logbookCfg.OutboundTLS.SkipVerify, tt.want)
			}
		})
	}
}
