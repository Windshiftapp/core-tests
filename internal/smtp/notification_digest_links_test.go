//go:build test

package smtp

import (
	"strings"
	"testing"
	"time"

	"windshift/internal/emailutil"
	"windshift/internal/models"
)

func TestAbsoluteActionURL(t *testing.T) {
	cases := []struct{ base, action, want string }{
		{"https://app.example.com", "/workspaces/1/items/2", "https://app.example.com/workspaces/1/items/2"},
		{"https://app.example.com/", "/workspaces/1/items/2", "https://app.example.com/workspaces/1/items/2"},
		{"https://app.example.com", "workspaces/1/items/2", "https://app.example.com/workspaces/1/items/2"},
		{"https://app.example.com", "https://other.example.com/x", "https://other.example.com/x"},
		{"", "/workspaces/1/items/2", "/workspaces/1/items/2"},
		{"https://app.example.com", "", ""},
	}
	for _, tc := range cases {
		if got := absoluteActionURL(tc.base, tc.action); got != tc.want {
			t.Errorf("absoluteActionURL(%q, %q) = %q, want %q", tc.base, tc.action, got, tc.want)
		}
	}
}

// TestNotificationDigestRendersActionLinks covers the bug hunt finding that
// digests had no clickable link: the action URL now renders in both parts.
func TestNotificationDigestRendersActionLinks(t *testing.T) {
	sender := &NotificationSMTPSender{}
	sender.SetBaseURL("https://app.example.com")
	notifications := []models.Notification{{
		Title:     "ITEM-42 was assigned to you",
		Message:   "Sam assigned this to you.",
		Type:      "assignment",
		Timestamp: time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC),
		ActionURL: "/workspaces/1/items/42",
	}}

	_, htmlBody, textBody, err := sender.RenderEmail(
		emailutil.TemplateNotificationBatch,
		buildNotificationBatchData(sender.baseURL, "Alex", notifications),
	)
	if err != nil {
		t.Fatalf("RenderEmail: %v", err)
	}
	const link = "https://app.example.com/workspaces/1/items/42"
	if !strings.Contains(htmlBody, `href="`+link+`"`) {
		t.Fatalf("HTML digest missing clickable link\n%s", htmlBody)
	}
	if !strings.Contains(textBody, link) {
		t.Fatalf("plain-text digest missing link: %q", textBody)
	}
}

func TestNotificationDigestOmitsLinkWhenActionURLMissing(t *testing.T) {
	sender := &NotificationSMTPSender{}
	notifications := []models.Notification{{
		Title:     "System notice",
		Message:   "No action.",
		Type:      "info",
		Timestamp: time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC),
	}}

	_, htmlBody, textBody, err := sender.RenderEmail(
		emailutil.TemplateNotificationBatch,
		buildNotificationBatchData("https://app.example.com", "Alex", notifications),
	)
	if err != nil {
		t.Fatalf("RenderEmail: %v", err)
	}
	if strings.Contains(htmlBody, "View in Windshift") || strings.Contains(textBody, "View:") {
		t.Fatalf("digest rendered a link for an empty action URL: html=%q text=%q", htmlBody, textBody)
	}
}
