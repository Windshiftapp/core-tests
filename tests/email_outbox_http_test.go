package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// seedOutboxReplyForComment inserts an email_reply_outbox row for an existing
// comment. The production API cannot create queued customer replies without a
// live inbound mailbox plus SMTP relay (the row only arises from the delivery
// pipeline), so this is the documented direct-write exception for exercising
// the operator boundary; items, comments, channels, and workspaces all come
// from the HTTP API.
func seedOutboxReplyForComment(t *testing.T, server *TestServer, channelID, commentID, itemID int, subject string) {
	t.Helper()
	if _, err := server.DB().ExecWrite(`
		INSERT INTO email_reply_outbox (
			comment_id, channel_id, item_id, to_email, to_name, subject,
			html_body, text_body, message_id, in_reply_to, references_json,
			from_email, from_name, attempt_count, next_attempt_at, created_at, updated_at
		) VALUES (?, ?, ?, 'customer@example.com', 'Customer One', ?,
			'<p>body</p>', 'body', ?, '', '[]',
			'noreply@test.com', 'Test App', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (comment_id, to_email) WHERE comment_id IS NOT NULL DO NOTHING
	`, commentID, channelID, itemID, subject, fmt.Sprintf("<ws-comment-%d@test.local>", commentID)); err != nil {
		t.Fatalf("seed outbox reply: %v", err)
	}
}

// createItemWithComments creates a ticket and comments through the production
// API, returning the item and its comment IDs.
func createItemWithComments(t *testing.T, server *TestServer, workspaceID int, count int) (itemID int, commentIDs []int) {
	t.Helper()
	itemID = CreateTestItem(t, server, workspaceID, "Outbox fixture")
	for i := 0; i < count; i++ {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", itemID), map[string]interface{}{
			"content":    fmt.Sprintf("agent reply fixture %d", i),
			"is_private": false,
		})
		AssertStatusCode(t, resp, http.StatusCreated)
		var payload struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode comment: %v", err)
		}
		resp.Body.Close()
		commentIDs = append(commentIDs, payload.ID)
	}
	return itemID, commentIDs
}

// seedOutboxReceipt inserts an intake_confirmation row. Receipts carry no
// comment_id, so they can only be addressed by outbox row id. The production
// API enqueues a receipt only through the inbound-mail pipeline, so this is the
// documented direct-write exception for exercising the operator boundary.
func seedOutboxReceipt(t *testing.T, server *TestServer, channelID, itemID int) int {
	t.Helper()
	var receiptID int
	if err := server.DB().QueryRow(`
		INSERT INTO email_reply_outbox (
			kind, comment_id, channel_id, item_id, to_email, to_name, subject,
			html_body, text_body, message_id, in_reply_to, references_json,
			from_email, from_name, attempt_count, next_attempt_at, created_at, updated_at
		) VALUES ('intake_confirmation', NULL, ?, ?, 'customer@example.com', 'Customer One', 'We received your request',
			'<p>body</p>', 'body', ?, '', '[]',
			'noreply@test.com', 'Test App', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, channelID, itemID, fmt.Sprintf("<ws-confirm-%d@test.local>", itemID)).Scan(&receiptID); err != nil {
		t.Fatalf("seed intake receipt: %v", err)
	}
	return receiptID
}

func setOutboxReceiptState(t *testing.T, server *TestServer, receiptID int, mutate string) {
	t.Helper()
	if _, err := server.DB().ExecWrite(fmt.Sprintf(
		"UPDATE email_reply_outbox SET %s, updated_at = CURRENT_TIMESTAMP WHERE id = ?", mutate,
	), receiptID); err != nil {
		t.Fatalf("set outbox receipt state %d: %v", receiptID, err)
	}
}

func setOutboxState(t *testing.T, server *TestServer, commentID int, mutate string) {
	t.Helper()
	if _, err := server.DB().ExecWrite(fmt.Sprintf(
		"UPDATE email_reply_outbox SET %s, updated_at = CURRENT_TIMESTAMP WHERE comment_id = ?", mutate,
	), commentID); err != nil {
		t.Fatalf("set outbox state %d: %v", commentID, err)
	}
}

// seedOutboxItem creates an item in the channel's workspace so outbox rows
// can reference a real ticket for the workspace redaction checks.
func getOutboxWorkspaceID(t *testing.T, server *TestServer, channelID int) int {
	t.Helper()
	var configJSON string
	if err := server.DB().QueryRow(`SELECT COALESCE(config, '{}') FROM channels WHERE id = ?`, channelID).Scan(&configJSON); err != nil {
		t.Fatalf("read channel config: %v", err)
	}
	var config struct {
		WorkspaceID int `json:"email_workspace_id"`
	}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("parse channel config: %v", err)
	}
	if config.WorkspaceID == 0 {
		t.Fatal("channel fixture has no email_workspace_id")
	}
	return config.WorkspaceID
}

// TestEmailReplyOutboxLifecycle verifies the operator boundary for queued
// customer replies: filtered listing, retry conflicts without SMTP or for
// terminal rows, durable discard, and redacted metadata only (never bodies).
func TestEmailReplyOutboxLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createRateLimitedEmailChannel(t, server, "Outbox lifecycle")
	workspaceID := getOutboxWorkspaceID(t, server, channelID)
	itemID, commentIDs := createItemWithComments(t, server, workspaceID, 4)

	for _, id := range commentIDs {
		if id == 0 {
			t.Fatal("comment fixture returned zero id")
		}
	}
	seedOutboxReplyForComment(t, server, channelID, commentIDs[0], itemID, "Re: pending reply")
	seedOutboxReplyForComment(t, server, channelID, commentIDs[1], itemID, "Re: delivered reply")
	setOutboxState(t, server, commentIDs[1], "delivered_at = CURRENT_TIMESTAMP")
	seedOutboxReplyForComment(t, server, channelID, commentIDs[2], itemID, "Re: failed reply")
	// A failed row sits in the backoff schedule with its last error recorded.
	setOutboxState(t, server, commentIDs[2], fmt.Sprintf(
		"attempt_count = 2, last_error = 'smtp relay down', next_attempt_at = '%s'",
		time.Now().Add(time.Hour).UTC().Format("2006-01-02 15:04:05"),
	))
	seedOutboxReplyForComment(t, server, channelID, commentIDs[3], itemID, "Re: discarded reply")
	setOutboxState(t, server, commentIDs[3], "discarded_at = CURRENT_TIMESTAMP")

	pendingID, deliveredID, failedID, discardedID := commentIDs[0], commentIDs[1], commentIDs[2], commentIDs[3]

	repliesPath := fmt.Sprintf("/channels/%d/email/replies", channelID)

	t.Run("pending filter is the operator queue", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, repliesPath, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Replies []struct {
				CommentID int    `json:"comment_id"`
				ToEmail   string `json:"to_email"`
				Subject   string `json:"subject"`
			} `json:"replies"`
			Total int `json:"total"`
		}
		DecodeJSON(t, resp, &body)
		if body.Total != 2 {
			t.Fatalf("pending total = %d, want 2", body.Total)
		}
		gotten := map[int]bool{}
		for _, reply := range body.Replies {
			gotten[reply.CommentID] = true
			if reply.ToEmail != "customer@example.com" {
				t.Fatalf("to_email = %q, want the customer address", reply.ToEmail)
			}
			if reply.Subject == "" {
				t.Fatal("subject missing for admin viewer")
			}
		}
		if !gotten[pendingID] || !gotten[failedID] {
			t.Fatalf("pending replies = %v, want %d and %d", gotten, pendingID, failedID)
		}
	})

	t.Run("status filters partition the queue", func(t *testing.T) {
		for status, want := range map[string]int{"delivered": 1, "discarded": 1, "all": 4} {
			resp := MakeAuthRequest(t, server, http.MethodGet, repliesPath+"?status="+status, nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusOK)
			var body struct {
				Total int `json:"total"`
			}
			DecodeJSON(t, resp, &body)
			if body.Total != want {
				t.Fatalf("status=%s total = %d, want %d", status, body.Total, want)
			}
		}
	})

	t.Run("invalid status is rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, repliesPath+"?status=bogus", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("retry without SMTP conflicts", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("%s/%d/retry", repliesPath, pendingID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)

		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		DecodeJSON(t, resp, &body)
		if body.Code != "SMTP_NOT_CONFIGURED" {
			t.Fatalf("error code = %q, want SMTP_NOT_CONFIGURED", body.Code)
		}
	})

	t.Run("delivered and discarded rows refuse retries", func(t *testing.T) {
		for _, commentID := range []int{deliveredID, discardedID} {
			resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("%s/%d/retry", repliesPath, commentID), nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusConflict)
		}
	})

	t.Run("missing reply is not found", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, repliesPath+"/999999/retry", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("discard is durable and one-shot", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("%s/%d/discard", repliesPath, pendingID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var discarded int
		if err := server.DB().QueryRow(
			`SELECT COUNT(*) FROM email_reply_outbox WHERE comment_id = ? AND discarded_at IS NOT NULL`,
			pendingID,
		).Scan(&discarded); err != nil || discarded != 1 {
			t.Fatalf("discarded rows = %d (err %v), want 1", discarded, err)
		}

		repeat := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("%s/%d/discard", repliesPath, pendingID), nil)
		defer repeat.Body.Close()
		AssertStatusCode(t, repeat, http.StatusConflict)

		// The discarded row left the pending queue.
		pending := MakeAuthRequest(t, server, http.MethodGet, repliesPath, nil)
		defer pending.Body.Close()
		AssertStatusCode(t, pending, http.StatusOK)
		var body struct {
			Total int `json:"total"`
		}
		DecodeJSON(t, pending, &body)
		if body.Total != 1 {
			t.Fatalf("pending total after discard = %d, want 1", body.Total)
		}
	})
}

// TestEmailReplyOutboxBulkDiscard verifies the one-action recovery for a burst
// of queued customer replies: the channel's whole pending queue is canceled in
// a single request, delivered and in-flight rows keep their state, and the
// operator boundary still denies users without channel management.
func TestEmailReplyOutboxBulkDiscard(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createRateLimitedEmailChannel(t, server, "Outbox bulk discard")
	workspaceID := getOutboxWorkspaceID(t, server, channelID)
	itemID, commentIDs := createItemWithComments(t, server, workspaceID, 4)

	seedOutboxReplyForComment(t, server, channelID, commentIDs[0], itemID, "Re: pending one")
	seedOutboxReplyForComment(t, server, channelID, commentIDs[1], itemID, "Re: pending two")
	seedOutboxReplyForComment(t, server, channelID, commentIDs[2], itemID, "Re: delivered")
	setOutboxState(t, server, commentIDs[2], "delivered_at = CURRENT_TIMESTAMP")
	seedOutboxReplyForComment(t, server, channelID, commentIDs[3], itemID, "Re: in flight")
	setOutboxState(t, server, commentIDs[3], fmt.Sprintf(
		"lease_owner = 'worker-a', next_attempt_at = '%s'",
		time.Now().Add(time.Hour).UTC().Format("2006-01-02 15:04:05"),
	))
	receiptID := seedOutboxReceipt(t, server, channelID, CreateTestItem(t, server, workspaceID, "Bulk discard receipt"))

	discardPath := fmt.Sprintf("/channels/%d/email/replies/discard-pending", channelID)

	t.Run("non-manager is denied", func(t *testing.T) {
		_, username, password := CreateTestUserWithCredentials(t, server, "outbox_bulk_user", "outbox-bulk-user@test.com")
		nonManagerToken := CreateBearerTokenForUser(t, server, username, password)
		resp := MakeAuthRequestWithToken(t, server, nonManagerToken, http.MethodPost, discardPath, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("pending queue is canceled in one request", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, discardPath, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Discarded int `json:"discarded"`
		}
		DecodeJSON(t, resp, &body)
		if body.Discarded != 3 {
			t.Fatalf("discarded = %d, want 3 (two replies and the receipt)", body.Discarded)
		}

		var pending int
		if err := server.DB().QueryRow(`
			SELECT COUNT(*) FROM email_reply_outbox
			WHERE channel_id = ? AND delivered_at IS NULL AND discarded_at IS NULL
		`, channelID).Scan(&pending); err != nil || pending != 1 {
			t.Fatalf("pending rows = %d (err %v), want 1 (the in-flight row)", pending, err)
		}

		// Delivered and in-flight rows keep their exact state.
		var deliveredAt, deliveredDiscarded, inFlightDiscarded, receiptDiscarded *time.Time
		var leaseOwner *string
		if err := server.DB().QueryRow(`SELECT delivered_at, discarded_at FROM email_reply_outbox WHERE comment_id = ?`, commentIDs[2]).Scan(&deliveredAt, &deliveredDiscarded); err != nil {
			t.Fatalf("load delivered row: %v", err)
		}
		if deliveredAt == nil || deliveredDiscarded != nil {
			t.Fatalf("delivered row = delivered %v discarded %v, want delivered and not discarded", deliveredAt, deliveredDiscarded)
		}
		if err := server.DB().QueryRow(`SELECT lease_owner, discarded_at FROM email_reply_outbox WHERE comment_id = ?`, commentIDs[3]).Scan(&leaseOwner, &inFlightDiscarded); err != nil {
			t.Fatalf("load in-flight row: %v", err)
		}
		if leaseOwner == nil || *leaseOwner != "worker-a" || inFlightDiscarded != nil {
			t.Fatalf("in-flight row = lease %v discarded %v, want worker-a lease and not discarded", leaseOwner, inFlightDiscarded)
		}
		if err := server.DB().QueryRow(`SELECT discarded_at FROM email_reply_outbox WHERE id = ?`, receiptID).Scan(&receiptDiscarded); err != nil || receiptDiscarded == nil {
			t.Fatalf("receipt discarded_at = %v (err %v), want set", receiptDiscarded, err)
		}
	})

	t.Run("second request is a no-op", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, discardPath, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Discarded int `json:"discarded"`
		}
		DecodeJSON(t, resp, &body)
		if body.Discarded != 0 {
			t.Fatalf("second discard = %d, want 0", body.Discarded)
		}
	})

	t.Run("listing reports nothing left to discard", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/email/replies?status=all", channelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			DiscardableTotal int `json:"discardable_total"`
		}
		DecodeJSON(t, resp, &body)
		if body.DiscardableTotal != 0 {
			t.Fatalf("discardable_total = %d, want 0", body.DiscardableTotal)
		}
	})
}

// TestEmailReplyOutboxDiscardReceiptByID verifies an operator can cancel a
// stuck intake receipt through its outbox row id: the success path, the
// one-shot refusal, and the delivered/in-flight/unknown conflicts.
func TestEmailReplyOutboxDiscardReceiptByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createRateLimitedEmailChannel(t, server, "Receipt discard")
	workspaceID := getOutboxWorkspaceID(t, server, channelID)

	pendingReceipt := seedOutboxReceipt(t, server, channelID, CreateTestItem(t, server, workspaceID, "Receipt pending"))
	deliveredReceipt := seedOutboxReceipt(t, server, channelID, CreateTestItem(t, server, workspaceID, "Receipt delivered"))
	setOutboxReceiptState(t, server, deliveredReceipt, "delivered_at = CURRENT_TIMESTAMP")
	inFlightReceipt := seedOutboxReceipt(t, server, channelID, CreateTestItem(t, server, workspaceID, "Receipt in flight"))
	setOutboxReceiptState(t, server, inFlightReceipt, fmt.Sprintf(
		"lease_owner = 'worker-a', next_attempt_at = '%s'",
		time.Now().Add(time.Hour).UTC().Format("2006-01-02 15:04:05"),
	))

	// The receipt must be visible as a row with an id and no comment_id, which
	// is what makes the row-scoped discard addressable at all.
	t.Run("listing exposes the receipt row id without a comment id", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/email/replies?status=all", channelID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Replies []struct {
				ID        int  `json:"id"`
				CommentID *int `json:"comment_id"`
			} `json:"replies"`
		}
		DecodeJSON(t, resp, &body)
		found := false
		for _, reply := range body.Replies {
			if reply.ID != pendingReceipt {
				continue
			}
			found = true
			if reply.CommentID != nil {
				t.Fatalf("receipt comment_id = %d, want omitted", *reply.CommentID)
			}
		}
		if !found {
			t.Fatalf("receipt row %d missing from the outbox listing", pendingReceipt)
		}
	})

	discardPath := func(replyID int) string {
		return fmt.Sprintf("/channels/%d/email/outbox/%d/discard", channelID, replyID)
	}

	t.Run("cancel is durable and one-shot", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, discardPath(pendingReceipt), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Discarded bool `json:"discarded"`
			ReplyID   int  `json:"reply_id"`
		}
		DecodeJSON(t, resp, &body)
		if !body.Discarded || body.ReplyID != pendingReceipt {
			t.Fatalf("discard response = %+v, want discarded receipt %d", body, pendingReceipt)
		}

		var discarded int
		if err := server.DB().QueryRow(
			`SELECT COUNT(*) FROM email_reply_outbox WHERE id = ? AND discarded_at IS NOT NULL`,
			pendingReceipt,
		).Scan(&discarded); err != nil || discarded != 1 {
			t.Fatalf("discarded receipt rows = %d (err %v), want 1", discarded, err)
		}

		repeat := MakeAuthRequest(t, server, http.MethodPost, discardPath(pendingReceipt), nil)
		defer repeat.Body.Close()
		AssertStatusCode(t, repeat, http.StatusConflict)
	})

	t.Run("delivered receipt refuses cancellation", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, discardPath(deliveredReceipt), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)

		var body struct {
			Code string `json:"code"`
		}
		DecodeJSON(t, resp, &body)
		if body.Code != "EMAIL_REPLY_NOT_DISCARDABLE" {
			t.Fatalf("error code = %q, want EMAIL_REPLY_NOT_DISCARDABLE", body.Code)
		}
	})

	t.Run("in-flight receipt refuses cancellation", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, discardPath(inFlightReceipt), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)

		var body struct {
			Code string `json:"code"`
		}
		DecodeJSON(t, resp, &body)
		if body.Code != "EMAIL_REPLY_SENDING" {
			t.Fatalf("error code = %q, want EMAIL_REPLY_SENDING", body.Code)
		}
	})

	t.Run("unknown receipt id is not discardable", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, discardPath(999999), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)
	})
}

// TestEmailReplyOutboxRequiresChannelManagement verifies the denial contract
// for the reply-queue surface.
func TestEmailReplyOutboxRequiresChannelManagement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createRateLimitedEmailChannel(t, server, "Outbox denial")
	workspaceID := getOutboxWorkspaceID(t, server, channelID)
	itemID, commentIDs := createItemWithComments(t, server, workspaceID, 1)
	seedOutboxReplyForComment(t, server, channelID, commentIDs[0], itemID, "Re: denial fixture")
	deniedCommentID := commentIDs[0]
	deniedReceiptID := seedOutboxReceipt(t, server, channelID, CreateTestItem(t, server, workspaceID, "Receipt denial"))

	_, otherUsername, otherPassword := CreateTestUserWithCredentials(
		t, server, "outbox_other", "outbox-other@test.com",
	)
	otherToken := CreateBearerTokenForUser(t, server, otherUsername, otherPassword)

	for _, tc := range []struct {
		name     string
		method   string
		endpoint string
	}{
		{"list denied", http.MethodGet, fmt.Sprintf("/channels/%d/email/replies", channelID)},
		{"retry denied", http.MethodPost, fmt.Sprintf("/channels/%d/email/replies/%d/retry", channelID, deniedCommentID)},
		{"discard denied", http.MethodPost, fmt.Sprintf("/channels/%d/email/replies/%d/discard", channelID, deniedCommentID)},
		{"receipt discard denied", http.MethodPost, fmt.Sprintf("/channels/%d/email/outbox/%d/discard", channelID, deniedReceiptID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeAuthRequestWithToken(t, server, otherToken, tc.method, tc.endpoint, nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusForbidden)
		})
	}

	// Denials must not have mutated the queue.
	var mutated int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM email_reply_outbox WHERE comment_id = ? AND (discarded_at IS NOT NULL OR delivered_at IS NOT NULL)`,
		deniedCommentID,
	).Scan(&mutated); err != nil || mutated != 0 {
		t.Fatalf("mutated rows after denial = %d (err %v), want 0", mutated, err)
	}
	var mutatedReceipts int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM email_reply_outbox WHERE id = ? AND (discarded_at IS NOT NULL OR delivered_at IS NOT NULL)`,
		deniedReceiptID,
	).Scan(&mutatedReceipts); err != nil || mutatedReceipts != 0 {
		t.Fatalf("mutated receipts after denial = %d (err %v), want 0", mutatedReceipts, err)
	}
}

// The outbox is per-recipient: one comment can produce several rows. Each row
// must carry its own id so a client can key them uniquely. The Processing Log
// keyed rows by comment_id and crashed Svelte with each_key_duplicate.
func TestEmailReplyOutboxListReturnsUniqueRowIDs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	channelID := createRateLimitedEmailChannel(t, server, "Outbox ids")
	workspaceID := getOutboxWorkspaceID(t, server, channelID)
	itemID, commentIDs := createItemWithComments(t, server, workspaceID, 1)
	commentID := commentIDs[0]

	for i, to := range []string{"customer@example.com", "participant@example.com"} {
		if _, err := server.DB().ExecWrite(`
			INSERT INTO email_reply_outbox (
				comment_id, channel_id, item_id, to_email, to_name, subject,
				html_body, text_body, message_id, in_reply_to, references_json,
				from_email, from_name, attempt_count, next_attempt_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, 'Recipient', 'Re: ids',
				'<p>body</p>', 'body', ?, '', '[]',
				'noreply@test.com', 'Test App', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, commentID, channelID, itemID, to, fmt.Sprintf("<ws-comment-%d-%d@test.local>", commentID, i)); err != nil {
			t.Fatalf("seed outbox recipient %d: %v", i, err)
		}
	}

	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/email/replies?status=all", channelID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var body struct {
		Replies []struct {
			ID        int `json:"id"`
			CommentID int `json:"comment_id"`
		} `json:"replies"`
	}
	DecodeJSON(t, resp, &body)
	if len(body.Replies) != 2 {
		t.Fatalf("replies = %d, want 2 (one per recipient)", len(body.Replies))
	}
	ids := map[int]bool{}
	for _, reply := range body.Replies {
		if reply.CommentID != commentID {
			t.Fatalf("comment_id = %d, want %d", reply.CommentID, commentID)
		}
		if reply.ID == 0 {
			t.Fatal("reply id is zero; the list must expose the outbox row id")
		}
		if ids[reply.ID] {
			t.Fatalf("duplicate reply id %d", reply.ID)
		}
		ids[reply.ID] = true
	}
}
