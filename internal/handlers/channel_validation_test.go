package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
)

func TestBuildSMTPTestEmailBodiesEscapesChannelNameInHTML(t *testing.T) {
	channelName := `<img src="https://evil.example/?x=1&y=2">`
	testTime := time.Date(2026, time.August, 9, 12, 34, 56, 0, time.UTC)

	htmlBody, textBody := buildSMTPTestEmailBodies(channelName, testTime)

	if strings.Contains(htmlBody, channelName) || strings.Contains(htmlBody, "<img") {
		t.Fatalf("HTML body contains unescaped channel name: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, "&lt;img") || !strings.Contains(htmlBody, "&amp;") {
		t.Fatalf("HTML body does not contain escaped channel name: %s", htmlBody)
	}
	if !strings.Contains(textBody, channelName) {
		t.Fatalf("text body lost the channel name: %s", textBody)
	}
}

func TestBareEmailAddressRejectsHeaderForms(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "bare", raw: "sender@example.com", want: "sender@example.com", ok: true},
		{name: "trimmed", raw: "  sender@example.com  ", want: "sender@example.com", ok: true},
		{name: "display name", raw: "Sender <sender@example.com>", ok: false},
		{name: "multiple", raw: "first@example.com, second@example.com", ok: false},
		{name: "newline", raw: "sender@example.com\r\nBcc: victim@example.com", ok: false},
		{name: "empty", raw: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := bareEmailAddress(tt.raw)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("bareEmailAddress(%q) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestValidatePortalPublicURLs(t *testing.T) {
	configFromJSON := func(raw string) models.ChannelConfig {
		t.Helper()
		var config models.ChannelConfig
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatalf("unmarshal config: %v", err)
		}
		return config
	}

	tests := []struct {
		name    string
		config  models.ChannelConfig
		wantErr bool
	}{
		{
			name: "uploaded assets and safe links",
			config: configFromJSON(`{
				"portal_background_image_url":"/api/portal-assets/12",
				"portal_logo_url":"https://cdn.example.com/logo.png",
				"portal_footer_columns":[{"links":[{"text":"Help","url":"/help"},{"text":"Mail","url":"mailto:help@example.com"}]}],
				"knowledge_base_share_link":"https://docs.example.com/share/abc"
			}`),
		},
		{
			name:    "executable footer link",
			config:  configFromJSON(`{"portal_footer_columns":[{"links":[{"text":"Bad","url":"javascript:alert(1)"}]}]}`),
			wantErr: true,
		},
		{
			name:    "protocol relative logo",
			config:  configFromJSON(`{"portal_logo_url":"//evil.example/logo.png"}`),
			wantErr: true,
		},
		{
			name:    "executable knowledge base share link",
			config:  configFromJSON(`{"knowledge_base_share_link":"javascript://example/share/abc"}`),
			wantErr: true,
		},
		{
			name:    "insecure knowledge base share link",
			config:  configFromJSON(`{"knowledge_base_share_link":"http://docs.example.com/share/abc"}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := services.ValidatePortalConfig(&tt.config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePortalPublicURLs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
