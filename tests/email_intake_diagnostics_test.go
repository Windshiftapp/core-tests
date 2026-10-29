package tests

import (
	"net/http"
	"testing"
	"time"
)

// seedSkippedSystemMail inserts a machine-generated tracking row directly.
// The production API cannot create intake state without a live IMAP mailbox,
// so this is the documented direct-write exception for exercising the
// diagnostics read path; the channel fixture itself is created through the API.
func seedSkippedSystemMail(t *testing.T, server *TestServer, channelID int, dedupKey, from, subject, category string, processedAt time.Time) {
	t.Helper()
	if _, err := server.DB().ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, from_name, subject, direction,
			 skipped_reason, completed_at, processed_at)
		VALUES (?, ?, ?, ?, 'System', ?, 'inbound', ?, CURRENT_TIMESTAMP, ?)
	`, channelID, "<"+dedupKey+"@example.com>", dedupKey, from, subject, category, processedAt); err != nil {
		t.Fatalf("seed skipped system mail row: %v", err)
	}
}

// TestEmailIntakeDiagnosticsListsSkippedSystemMail verifies that
// machine-generated mail which intake did not turn into a ticket stays
// observable through the admin diagnostics endpoint, with category counts and
// the mailbox it arrived on.
func TestEmailIntakeDiagnosticsListsSkippedSystemMail(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createInboundEmailChannelFixture(t, server, "System mail diagnostics", nil)
	seedSkippedSystemMail(t, server, channelID, "sys-bounce-1",
		"postmaster@outlook.com", "Undeliverable: notifications", "delivery_failure", time.Now().UTC().Add(-time.Hour))
	seedSkippedSystemMail(t, server, channelID, "sys-bounce-2",
		"mailer-daemon@mx.example.com", "Delivery Status Notification", "delivery_failure", time.Now().UTC().Add(-2*time.Hour))
	seedSkippedSystemMail(t, server, channelID, "sys-ooo-1",
		"customer@example.com", "Automatic reply: Out of office", "auto_reply", time.Now().UTC().Add(-3*time.Hour))
	// Outside the 24h window; must not be counted or listed.
	seedSkippedSystemMail(t, server, channelID, "sys-old-1",
		"postmaster@outlook.com", "Undeliverable: old", "delivery_failure", time.Now().UTC().Add(-72*time.Hour))

	resp := MakeAuthRequest(t, server, http.MethodGet, "/admin/diagnostics/email-intake?since=24h", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var body struct {
		Counts []struct {
			Category string `json:"category"`
			Count    int    `json:"count"`
		} `json:"counts"`
		Messages []struct {
			ID          int    `json:"id"`
			ChannelID   int    `json:"channel_id"`
			ChannelName string `json:"channel_name"`
			FromEmail   string `json:"from_email"`
			Subject     string `json:"subject"`
			Category    string `json:"category"`
		} `json:"messages"`
	}
	DecodeJSON(t, resp, &body)

	counts := map[string]int{}
	for _, entry := range body.Counts {
		counts[entry.Category] = entry.Count
	}
	if counts["delivery_failure"] != 2 || counts["auto_reply"] != 1 {
		t.Fatalf("counts = %+v, want delivery_failure=2 auto_reply=1", counts)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(body.Messages))
	}
	for _, message := range body.Messages {
		if message.ChannelID != channelID {
			t.Fatalf("message channel = %d, want %d", message.ChannelID, channelID)
		}
		if message.ChannelName != "System mail diagnostics" {
			t.Fatalf("channel name = %q, want %q", message.ChannelName, "System mail diagnostics")
		}
		if message.Subject == "Undeliverable: old" {
			t.Fatal("message outside the since window was listed")
		}
	}
}

// TestEmailIntakeDiagnosticsRequiresSystemAdmin pins the denial contract: the
// diagnostics view is admin-only.
func TestEmailIntakeDiagnosticsRequiresSystemAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	_, username, password := CreateTestUserWithCredentials(
		t, server, "email_intake_diag_user", "email-intake-diag-user@test.com",
	)
	nonAdminToken := CreateBearerTokenForUser(t, server, username, password)

	resp := MakeAuthRequestWithToken(t, server, nonAdminToken, http.MethodGet, "/admin/diagnostics/email-intake", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusForbidden)

	adminResp := MakeAuthRequest(t, server, http.MethodGet, "/admin/diagnostics/email-intake", nil)
	defer adminResp.Body.Close()
	AssertStatusCode(t, adminResp, http.StatusOK)

	var body struct {
		Counts   []any `json:"counts"`
		Messages []any `json:"messages"`
	}
	DecodeJSON(t, adminResp, &body)
	if body.Counts == nil || body.Messages == nil {
		t.Fatalf("empty diagnostics must serialize as arrays, got counts=%v messages=%v", body.Counts, body.Messages)
	}
}
