package webauthn

import (
	"errors"
	"reflect"
	"testing"
)

func TestNewConfigAcceptsLocalAndDottedRPIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rpID string
	}{
		{name: "localhost", rpID: "localhost"},
		{name: "IPv4 address", rpID: "192.0.2.10"},
		{name: "dotted homelab name", rpID: "windshift.home.arpa"},
		{name: "public domain", rpID: "windshift.example.com"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config, err := NewConfig(Options{
				RPID:        tt.rpID,
				RPName:      "Windshift Test",
				Origins:     []string{"https://example.test"},
				EnableHTTPS: true,
			})
			if err != nil {
				t.Fatalf("NewConfig(%q): %v", tt.rpID, err)
			}
			if config.RPID != tt.rpID {
				t.Fatalf("RPID = %q, want %q", config.RPID, tt.rpID)
			}
		})
	}
}

// A TLS-terminating proxy is the case that motivated deriving origins from the
// base URL: the browser only ever sees the public origin, never the listen port
// the server was configured with.
func TestNewConfigDerivesOriginFromBaseURLWithoutAllowedHosts(t *testing.T) {
	t.Parallel()

	config, err := NewConfig(Options{
		RPID:     "project.example.com",
		RPName:   "Windshift Test",
		BaseURL:  "https://project.example.com",
		Port:     "8080",
		UseProxy: true,
	})
	if err != nil {
		t.Fatalf("NewConfig() with only a base URL: %v", err)
	}

	want := []string{"https://project.example.com"}
	if !reflect.DeepEqual(config.RPOrigins, want) {
		t.Fatalf("RPOrigins = %q, want %q", config.RPOrigins, want)
	}
}

func TestNewConfigDropsBaseURLContextPathFromOrigin(t *testing.T) {
	t.Parallel()

	config, err := NewConfig(Options{
		RPID:     "project.example.com",
		RPName:   "Windshift Test",
		BaseURL:  "https://project.example.com/windshift",
		UseProxy: true,
	})
	if err != nil {
		t.Fatalf("NewConfig() with a context path: %v", err)
	}

	want := []string{"https://project.example.com"}
	if !reflect.DeepEqual(config.RPOrigins, want) {
		t.Fatalf("RPOrigins = %q, want %q", config.RPOrigins, want)
	}
}

func TestNewConfigKeepsBaseURLPortInOrigin(t *testing.T) {
	t.Parallel()

	config, err := NewConfig(Options{
		RPID:    "project.example.com",
		RPName:  "Windshift Test",
		BaseURL: "https://project.example.com:8443",
		Port:    "8080",
	})
	if err != nil {
		t.Fatalf("NewConfig() with a non-standard base URL port: %v", err)
	}

	want := []string{"https://project.example.com:8443"}
	if !reflect.DeepEqual(config.RPOrigins, want) {
		t.Fatalf("RPOrigins = %q, want %q", config.RPOrigins, want)
	}
}

// Installations reachable under more than one name must keep every allowed host
// as an origin, with the base URL leading the list.
func TestNewConfigCombinesBaseURLAndAllowedHostOrigins(t *testing.T) {
	t.Parallel()

	config, err := NewConfig(Options{
		RPID:         "project.example.com",
		RPName:       "Windshift Test",
		BaseURL:      "https://project.example.com",
		AllowedHosts: "project.example.com, project.internal",
		Port:         "8080",
		UseProxy:     true,
	})
	if err != nil {
		t.Fatalf("NewConfig() with base URL and allowed hosts: %v", err)
	}

	want := []string{
		"https://project.example.com",
		"https://project.example.com:8080",
		"https://project.example.com:443",
		"https://project.internal:8080",
		"https://project.internal:443",
	}
	if !reflect.DeepEqual(config.RPOrigins, want) {
		t.Fatalf("RPOrigins = %q, want %q", config.RPOrigins, want)
	}
}

func TestNewConfigAcceptsURLFormAllowedHosts(t *testing.T) {
	t.Parallel()

	config, err := NewConfig(Options{
		RPID:         "project.example.com",
		RPName:       "Windshift Test",
		AllowedHosts: "https://project.example.com",
		Port:         "443",
		UseProxy:     true,
	})
	if err != nil {
		t.Fatalf("NewConfig() with URL-form allowed host: %v", err)
	}

	want := []string{"https://project.example.com"}
	if !reflect.DeepEqual(config.RPOrigins, want) {
		t.Fatalf("RPOrigins = %q, want %q", config.RPOrigins, want)
	}
}

func TestNewConfigRejectsNonOriginAllowedHostURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		allowedHost string
	}{
		{name: "path", allowedHost: "https://project.example.com/windshift"},
		{name: "query", allowedHost: "https://project.example.com?tenant=other"},
		{name: "credentials", allowedHost: "https://admin@project.example.com"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewConfig(Options{
				RPID:         "project.example.com",
				RPName:       "Windshift Test",
				AllowedHosts: tt.allowedHost,
				Port:         "443",
				UseProxy:     true,
			})
			if err == nil {
				t.Fatalf("NewConfig() accepted non-origin allowed host %q", tt.allowedHost)
			}
		})
	}
}

func TestNewConfigClassifiesSingleLabelRPIDAsOptionalCapabilityError(t *testing.T) {
	t.Parallel()

	_, err := NewConfig(Options{
		RPID:         "windshift",
		RPName:       "Windshift Test",
		Origins:      []string{"https://windshift"},
		AllowedHosts: "windshift",
		Port:         "443",
		EnableHTTPS:  true,
	})
	if err == nil {
		t.Fatal("NewConfig(windshift) returned nil error")
	}

	var invalidRPIDErr *InvalidRPIDError
	if !errors.As(err, &invalidRPIDErr) {
		t.Fatalf("error = %T %v, want InvalidRPIDError", err, err)
	}
	if invalidRPIDErr.RPID != "windshift" {
		t.Fatalf("InvalidRPIDError.RPID = %q, want %q", invalidRPIDErr.RPID, "windshift")
	}
}

// Without any origin the relying party cannot verify a ceremony, but the server
// must stay up with passkeys disabled, so the failure is typed rather than
// generic.
func TestNewConfigClassifiesMissingOriginsAsOptionalCapabilityError(t *testing.T) {
	t.Parallel()

	_, err := NewConfig(Options{
		RPID:   "project.example.com",
		RPName: "Windshift Test",
		Port:   "8080",
	})
	if err == nil {
		t.Fatal("NewConfig() without a base URL or allowed hosts returned nil error")
	}

	var missingOriginsErr *MissingOriginsError
	if !errors.As(err, &missingOriginsErr) {
		t.Fatalf("error = %T %v, want MissingOriginsError", err, err)
	}
}

// A base URL that is not an absolute http(s) URL cannot yield an origin; it must
// not silently produce a broken one.
func TestNewConfigIgnoresSchemelessBaseURL(t *testing.T) {
	t.Parallel()

	_, err := NewConfig(Options{
		RPID:    "project.example.com",
		RPName:  "Windshift Test",
		BaseURL: "project.example.com",
	})

	var missingOriginsErr *MissingOriginsError
	if !errors.As(err, &missingOriginsErr) {
		t.Fatalf("error = %T %v, want MissingOriginsError", err, err)
	}
}
