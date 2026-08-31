package config

import (
	"errors"
	"testing"
)

func TestResolveWebAuthnRPID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		explicit         string
		baseURL          string
		fallbackHostname string
		fallbackErr      error
		want             string
	}{
		{
			name:             "localhost public URL",
			baseURL:          "http://localhost:8080",
			fallbackHostname: "container-id",
			want:             "localhost",
		},
		{
			name:             "domain public URL",
			baseURL:          "https://windshift.example.com/app",
			fallbackHostname: "container-id",
			want:             "windshift.example.com",
		},
		{
			name:             "explicit override wins",
			explicit:         "login.example.com",
			baseURL:          "https://windshift.example.com",
			fallbackHostname: "container-id",
			want:             "login.example.com",
		},
		{
			name:             "explicit URL is normalized to its hostname",
			explicit:         "https://project.example.com",
			baseURL:          "https://windshift.example.com",
			fallbackHostname: "container-id",
			want:             "project.example.com",
		},
		{
			name:             "explicit URL with credentials is not normalized",
			explicit:         "https://admin@project.example.com",
			baseURL:          "https://windshift.example.com",
			fallbackHostname: "container-id",
			want:             "https://admin@project.example.com",
		},
		{
			name:             "missing public URL falls back to host",
			fallbackHostname: "container-id",
			want:             "container-id",
		},
		{
			name:             "invalid public URL falls back to host",
			baseURL:          "://not-a-url",
			fallbackHostname: "container-id",
			want:             "container-id",
		},
		{
			name:        "hostname lookup failure leaves RP ID empty",
			fallbackErr: errors.New("hostname unavailable"),
			want:        "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveWebAuthnRPID(tt.explicit, tt.baseURL, func() (string, error) {
				return tt.fallbackHostname, tt.fallbackErr
			})
			if got != tt.want {
				t.Fatalf("resolveWebAuthnRPID() = %q, want %q", got, tt.want)
			}
		})
	}
}
