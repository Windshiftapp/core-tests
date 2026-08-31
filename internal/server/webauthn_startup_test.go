//go:build test

package server

import (
	"testing"

	"windshift/internal/config"
)

func TestInitializeWebAuthnConfigsDisablesUnsupportedLocalRPID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rpID       string
		wantActive bool
	}{
		{name: "single-label homelab service", rpID: "windshift", wantActive: false},
		{name: "localhost", rpID: "localhost", wantActive: true},
		{name: "local IP", rpID: "192.0.2.10", wantActive: true},
		{name: "dotted homelab name", rpID: "windshift.home.arpa", wantActive: true},
		{name: "public domain", rpID: "windshift.example.com", wantActive: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{
				AllowedHosts: tt.rpID,
				WebAuthn: config.WebAuthnConfig{
					RPID:   tt.rpID,
					RPName: "Windshift Test",
				},
			}
			internalConfig, portalConfig, err := initializeWebAuthnConfigs(cfg, false, "443", true)
			if err != nil {
				t.Fatalf("initializeWebAuthnConfigs(): %v", err)
			}

			active := internalConfig != nil && portalConfig != nil
			if active != tt.wantActive {
				t.Fatalf("active = %v, want %v (internal=%v portal=%v)", active, tt.wantActive, internalConfig != nil, portalConfig != nil)
			}
		})
	}
}
