package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// seedRateLimitedEmail inserts a flood-declined tracking row directly. The
// production API cannot create rate-limited intake state without a live IMAP
// mailbox, so this is the documented direct-write exception for exercising
// the requeue boundary; channel and workspace fixtures still go through the
// HTTP API.
func seedRateLimitedEmail(t *testing.T, server *TestServer, channelID int, dedupKey string, uid, uidValidity int) {
	t.Helper()
	if _, err := server.DB().ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, from_name, subject, direction, uid, uid_validity, rate_limited_at, processed_at)
		VALUES (?, ?, ?, 'flooder@example.com', 'Flooder', 'Flood', 'inbound', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, channelID, "<"+dedupKey+"@example.com>", dedupKey, uid, uidValidity); err != nil {
		t.Fatalf("seed rate-limited tracking row: %v", err)
	}
}

func setEmailWatermark(t *testing.T, server *TestServer, channelID, lastUID int) {
	t.Helper()
	// API-created channels have no state row until the scheduler polls them;
	// upsert so the endpoints under test see the watermark the fake poll left.
	if _, err := server.DB().ExecWrite(`
		INSERT INTO email_channel_state (channel_id, last_uid, uid_validity)
		VALUES (?, ?, 42)
		ON CONFLICT(channel_id) DO UPDATE SET last_uid = excluded.last_uid, uid_validity = 42
	`, channelID, lastUID); err != nil {
		t.Fatalf("set email watermark: %v", err)
	}
}

func getEmailWatermark(t *testing.T, server *TestServer, channelID int) (lastUID, uidValidity int) {
	t.Helper()
	if err := server.DB().QueryRow(
		`SELECT last_uid, COALESCE(uid_validity, 0) FROM email_channel_state WHERE channel_id = ?`,
		channelID,
	).Scan(&lastUID, &uidValidity); err != nil {
		t.Fatalf("read email watermark: %v", err)
	}
	return lastUID, uidValidity
}

var emailFixtureSeq int

// createInboundEmailChannelFixture creates a fully-wired inbound email
// channel through the production API: workspace, configuration-set
// association with a valid item type, provider, and the channel itself.
func createInboundEmailChannelFixture(t *testing.T, server *TestServer, name string, rateLimitPerHour *int) int {
	t.Helper()

	// Workspace keys are unique per server and limited to 10 alphanumeric
	// characters, so build one from the fixture sequence.
	emailFixtureSeq++
	key := fmt.Sprintf("EM%d", emailFixtureSeq)
	workspaceID, _ := CreateTestWorkspace(t, server, name+" WS", key)
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	channelID := CreateInboundEmailChannel(t, server, EmailChannelConfig{
		Name:             name,
		WorkspaceID:      workspaceID,
		ItemTypeID:       itemTypeID,
		EmailProviderID:  CreateEmailProvider(t, server, name+" Provider", "generic"),
		IMAPHost:         "localhost",
		IMAPPort:         993,
		Username:         "testuser",
		Password:         "testpass",
		Encryption:       "ssl",
		RateLimitPerHour: rateLimitPerHour,
	})

	// The create endpoint builds its own (mostly empty) config; the real
	// intake settings land through the dedicated config-update endpoint. The
	// values satisfy ValidateConfigForEnable, but no IMAP connection is ever
	// dialed by these fixtures.
	channelConfig := map[string]any{
		"email_workspace_id": workspaceID,
		"email_item_type_id": itemTypeID,
		"email_auth_method":  "basic",
		"email_mailbox":      "INBOX",
		"email_mark_as_read": true,
		"imap_host":          "localhost",
		"imap_port":          993,
		"imap_username":      "testuser",
		"imap_password":      "testpass",
		"imap_encryption":    "ssl",
	}
	if rateLimitPerHour != nil {
		channelConfig["email_rate_limit_per_hour"] = *rateLimitPerHour
	}
	UpdateChannelConfig(t, server, channelID, channelConfig)

	return channelID
}

func createRateLimitedEmailChannel(t *testing.T, server *TestServer, name string) int {
	cap := 1
	return createInboundEmailChannelFixture(t, server, name, &cap)
}

// TestRequeueRateLimitedEmails verifies the flood-recovery boundary: the log
// reports declined mail, the requeue endpoint rewinds the watermark to the
// earliest declined UID in the current epoch, and repeated calls stay
// idempotent. A channel with nothing declined reports requeued=false.
func TestRequeueRateLimitedEmails(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createRateLimitedEmailChannel(t, server, "Rate limit recovery")
	// The scheduler consumed UIDs up to 2 and declined the message at UID 2.
	setEmailWatermark(t, server, channelID, 2)
	seedRateLimitedEmail(t, server, channelID, "rate-1", 2, 42)

	t.Run("email log surfaces rate-limited mail", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/email-log", channelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			State struct {
				RateLimitedCount int `json:"rate_limited_count"`
			} `json:"state"`
			Messages []struct {
				ItemID        *int       `json:"item_id"`
				RateLimitedAt *time.Time `json:"rate_limited_at"`
				FromEmail     string     `json:"from_email"`
			} `json:"messages"`
		}
		DecodeJSON(t, resp, &body)
		if body.State.RateLimitedCount != 1 {
			t.Fatalf("rate_limited_count = %d, want 1", body.State.RateLimitedCount)
		}
		if len(body.Messages) != 1 {
			t.Fatalf("message rows = %d, want 1", len(body.Messages))
		}
		msg := body.Messages[0]
		if msg.RateLimitedAt == nil {
			t.Fatal("message row missing rate_limited_at")
		}
		if msg.ItemID != nil {
			t.Fatalf("rate-limited row has item_id %d, want null", *msg.ItemID)
		}
		if msg.FromEmail != "flooder@example.com" {
			t.Fatalf("from_email = %q, want flooder@example.com", msg.FromEmail)
		}
	})

	t.Run("requeue rewinds watermark to earliest declined UID", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/email/requeue-rate-limited", channelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Requeued      bool `json:"requeued"`
			FromUID       int  `json:"from_uid"`
			RequeuedCount int  `json:"requeued_count"`
		}
		DecodeJSON(t, resp, &body)
		if !body.Requeued || body.FromUID != 2 {
			t.Fatalf("requeue = (requeued:%v from_uid:%d), want (true, 2)", body.Requeued, body.FromUID)
		}
		if body.RequeuedCount != 1 {
			t.Fatalf("requeued_count = %d, want 1", body.RequeuedCount)
		}
		lastUID, validity := getEmailWatermark(t, server, channelID)
		if lastUID != 1 || validity != 42 {
			t.Fatalf("watermark = %d/%d, want 1/42", lastUID, validity)
		}
		// The claim lease must be invalidated so the next poll reclaims the
		// message instead of deferring behind the 5-minute stale-claim window.
		var processedAt time.Time
		if err := server.DB().QueryRow(
			`SELECT processed_at FROM email_message_tracking WHERE channel_id = ? AND rate_limited_at IS NOT NULL`,
			channelID,
		).Scan(&processedAt); err != nil {
			t.Fatalf("read rate-limited processed_at: %v", err)
		}
		// Mirrors the processor's unexported 5-minute trackingClaimStaleAfter.
		if !processedAt.Before(time.Now().Add(-5 * time.Minute)) {
			t.Fatalf("processed_at = %v, want a claim older than the stale window", processedAt)
		}
	})

	t.Run("requeue is idempotent", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/email/requeue-rate-limited", channelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Requeued bool `json:"requeued"`
			FromUID  int  `json:"from_uid"`
		}
		DecodeJSON(t, resp, &body)
		if !body.Requeued || body.FromUID != 2 {
			t.Fatalf("second requeue = (requeued:%v from_uid:%d), want (true, 2)", body.Requeued, body.FromUID)
		}
	})

	t.Run("channel without declined mail reports nothing to requeue", func(t *testing.T) {
		emptyChannelID := createRateLimitedEmailChannel(t, server, "Rate limit empty")
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/email/requeue-rate-limited", emptyChannelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Requeued bool `json:"requeued"`
		}
		DecodeJSON(t, resp, &body)
		if body.Requeued {
			t.Fatal("empty channel reported requeued=true, want false")
		}
	})
}

// TestRequeueRateLimitedEmailsRequiresChannelManagement verifies the denial
// contract: a user without channel-management rights gets 403 for both the
// requeue action and the flood-visibility log.
func TestRequeueRateLimitedEmailsRequiresChannelManagement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createRateLimitedEmailChannel(t, server, "Rate limit denial")
	setEmailWatermark(t, server, channelID, 2)
	seedRateLimitedEmail(t, server, channelID, "rate-denied", 2, 42)

	_, otherUsername, otherPassword := CreateTestUserWithCredentials(
		t, server, "rate_limit_other", "rate-limit-other@test.com",
	)
	otherToken := CreateBearerTokenForUser(t, server, otherUsername, otherPassword)

	for _, tc := range []struct {
		name     string
		method   string
		endpoint string
	}{
		{"requeue denied", http.MethodPost, fmt.Sprintf("/channels/%d/email/requeue-rate-limited", channelID)},
		{"email log denied", http.MethodGet, fmt.Sprintf("/channels/%d/email-log", channelID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeAuthRequestWithToken(t, server, otherToken, tc.method, tc.endpoint, nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusForbidden)
		})
	}

	// The denial must not have mutated state.
	if lastUID, _ := getEmailWatermark(t, server, channelID); lastUID != 2 {
		t.Fatalf("watermark after denial = %d, want unchanged 2", lastUID)
	}
}

// seedRateLimitedIntakeEmail inserts a flood-declined tracking row owned by a
// specific intake. The production API cannot create rate-limited state without
// a live IMAP mailbox, so this is the documented direct-write exception.
func seedRateLimitedIntakeEmail(t *testing.T, server *TestServer, channelID, intakeID int, dedupKey string, uid, uidValidity int) {
	t.Helper()
	if _, err := server.DB().ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, intake_id, message_id, dedup_key, from_email, from_name, subject, direction, uid, uid_validity, rate_limited_at, processed_at)
		VALUES (?, ?, ?, ?, 'flooder@example.com', 'Flooder', 'Flood', 'inbound', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, channelID, intakeID, "<"+dedupKey+"@example.com>", dedupKey, uid, uidValidity); err != nil {
		t.Fatalf("seed rate-limited intake tracking row: %v", err)
	}
}

func setIntakeWatermark(t *testing.T, server *TestServer, intakeID, lastUID int) {
	t.Helper()
	if _, err := server.DB().ExecWrite(`
		INSERT INTO email_intake_state (intake_id, last_uid, uid_validity)
		VALUES (?, ?, 42)
		ON CONFLICT(intake_id) DO UPDATE SET last_uid = excluded.last_uid, uid_validity = 42
	`, intakeID, lastUID); err != nil {
		t.Fatalf("set intake watermark: %v", err)
	}
}

func getIntakeWatermark(t *testing.T, server *TestServer, intakeID int) (lastUID, uidValidity int) {
	t.Helper()
	if err := server.DB().QueryRow(
		`SELECT last_uid, COALESCE(uid_validity, 0) FROM email_intake_state WHERE intake_id = ?`,
		intakeID,
	).Scan(&lastUID, &uidValidity); err != nil {
		t.Fatalf("read intake watermark: %v", err)
	}
	return lastUID, uidValidity
}

// TestRequeueRateLimitedEmailsPerIntake covers the WI-1644 split: a mailbox
// with an intake keeps its cursor on email_intake_state, so requeue must rewind
// that row rather than the unused channel watermark. Pre-split rows
// (intake_id NULL) still use the channel watermark.
func TestRequeueRateLimitedEmailsPerIntake(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Intake requeue", "IRQ")
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	channelID := CreateInboundEmailChannel(t, server, EmailChannelConfig{
		Name:            "Intake requeue mailbox",
		WorkspaceID:     workspaceID,
		ItemTypeID:      itemTypeID,
		EmailProviderID: CreateEmailProvider(t, server, "Intake requeue provider", "generic"),
		IMAPHost:        "localhost",
		IMAPPort:        993,
		Username:        "testuser",
		Password:        "testpass",
		Encryption:      "ssl",
	})

	intakeResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", channelID), map[string]interface{}{
		"folder":       "INBOX",
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	})
	if intakeResp.StatusCode != http.StatusCreated && intakeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(intakeResp.Body)
		intakeResp.Body.Close()
		t.Fatalf("create intake: status=%d body=%s", intakeResp.StatusCode, body)
	}
	var intake map[string]interface{}
	DecodeJSON(t, intakeResp, &intake)
	intakeResp.Body.Close()
	intakeID := ExtractIDFromResponse(t, intake)

	// The intake advanced to UID 2 and declined the message there.
	setIntakeWatermark(t, server, intakeID, 2)
	seedRateLimitedIntakeEmail(t, server, channelID, intakeID, "rate-intake-1", 2, 42)
	// A pre-split row stays on the channel watermark.
	setEmailWatermark(t, server, channelID, 7)
	seedRateLimitedEmail(t, server, channelID, "rate-legacy-1", 7, 42)

	t.Run("email log reports per-intake watermark", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/email-log", channelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			State struct {
				Intakes []struct {
					IntakeID         int `json:"intake_id"`
					LastUID          int `json:"last_uid"`
					UIDValidity      int `json:"uid_validity"`
					RateLimitedCount int `json:"rate_limited_count"`
				} `json:"intakes"`
			} `json:"state"`
		}
		DecodeJSON(t, resp, &body)
		if len(body.State.Intakes) != 1 {
			t.Fatalf("intakes = %d, want 1", len(body.State.Intakes))
		}
		got := body.State.Intakes[0]
		if got.IntakeID != intakeID || got.LastUID != 2 || got.UIDValidity != 42 {
			t.Fatalf("intake state = %+v, want id=%d last_uid=2 validity=42", got, intakeID)
		}
		if got.RateLimitedCount != 1 {
			t.Fatalf("intake rate_limited_count = %d, want 1", got.RateLimitedCount)
		}
	})

	t.Run("requeue rewinds the intake and channel watermarks", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/email/requeue-rate-limited", channelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Requeued      bool `json:"requeued"`
			FromUID       int  `json:"from_uid"`
			RequeuedCount int  `json:"requeued_count"`
		}
		DecodeJSON(t, resp, &body)
		if !body.Requeued || body.FromUID != 2 || body.RequeuedCount != 2 {
			t.Fatalf("requeue = %+v, want requeued=true from_uid=2 count=2", body)
		}

		lastUID, validity := getIntakeWatermark(t, server, intakeID)
		if lastUID != 1 || validity != 42 {
			t.Fatalf("intake watermark = %d/%d, want 1/42", lastUID, validity)
		}
		channelLastUID, _ := getEmailWatermark(t, server, channelID)
		if channelLastUID != 6 {
			t.Fatalf("channel watermark = %d, want 6", channelLastUID)
		}
	})
}
