package utils

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOutboundTLSConfigKeepsVerificationSecureByDefault(t *testing.T) {
	SetSkipTLSVerify(false)
	t.Cleanup(func() { SetSkipTLSVerify(false) })

	verified := OutboundTLSConfig("service.internal.example")
	if verified.InsecureSkipVerify {
		t.Fatal("certificate verification is disabled by default")
	}
	if verified.ServerName != "service.internal.example" {
		t.Fatalf("ServerName = %q, want service.internal.example", verified.ServerName)
	}
	if verified.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS 1.2", verified.MinVersion)
	}

	SetSkipTLSVerify(true)
	insecure := OutboundTLSConfig("service.internal.example")
	if !insecure.InsecureSkipVerify {
		t.Fatal("certificate verification remains enabled after explicit bypass")
	}
}

func TestNewHTTPClientAcceptsSelfSignedCertificateOnlyWhenEnabled(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "self-signed accepted")
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { SetSkipTLSVerify(false) })

	SetSkipTLSVerify(false)
	verifiedClient := NewHTTPClient(time.Second)
	if response, err := verifiedClient.Get(server.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("self-signed HTTPS certificate was accepted while verification was enabled")
	}

	SetSkipTLSVerify(true)
	insecureClient := NewHTTPClient(time.Second)
	response, err := insecureClient.Get(server.URL)
	if err != nil {
		t.Fatalf("self-signed HTTPS certificate was rejected with verification bypass enabled: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "self-signed accepted" {
		t.Fatalf("response = (%d, %q), want (200, self-signed accepted)", response.StatusCode, body)
	}
}
