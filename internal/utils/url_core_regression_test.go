package utils

import "testing"

func TestValidateBrowserNavigationURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{url: "https://example.com/help"},
		{url: "mailto:help@example.com"},
		{url: "tel:+41440000000"},
		{url: "/help"},
		{url: "#support"},
		{url: "javascript:alert(1)", wantErr: true},
		{url: "data:text/html,hello", wantErr: true},
		{url: "//evil.example/path", wantErr: true},
		{url: "/\\evil.example/path", wantErr: true},
		{url: "https://user@example.com/path", wantErr: true},
		{url: " https://example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := ValidateBrowserNavigationURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateBrowserNavigationURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateBrowserAssetURL(t *testing.T) {
	for _, safeURL := range []string{"/api/portal-assets/1", "https://cdn.example.com/logo.png", "http://cdn.example.com/logo.png"} {
		if err := ValidateBrowserAssetURL(safeURL); err != nil {
			t.Errorf("ValidateBrowserAssetURL(%q) unexpected error: %v", safeURL, err)
		}
	}
	for _, unsafeURL := range []string{"javascript:alert(1)", "data:image/svg+xml,svg", "//evil.example/logo", "/\\evil.example/logo"} {
		if err := ValidateBrowserAssetURL(unsafeURL); err == nil {
			t.Errorf("ValidateBrowserAssetURL(%q) unexpectedly succeeded", unsafeURL)
		}
	}
}
