package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// TestLogAudit_DetailsMarshalFailure_WritesSentinel covers the recovery path
// when json.Marshal(event.Details) fails (e.g. when Details contains an
// unmarshalable value like a channel). The audit row must still be inserted,
// with a sentinel details payload that records the marshal error rather than
// silently storing NULL.
func TestLogAudit_DetailsMarshalFailure_WritesSentinel(t *testing.T) {
	db, err := database.NewSQLiteDB("file:logger_audit_marshal_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecWrite(`CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		user_id INTEGER,
		username TEXT NOT NULL,
		ip_address TEXT,
		user_agent TEXT,
		action_type TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		resource_id INTEGER,
		resource_name TEXT,
		details TEXT,
		success BOOLEAN NOT NULL DEFAULT TRUE,
		error_message TEXT,
		source TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create audit_logs: %v", err)
	}

	// channels can't be JSON-marshaled — triggers the sentinel branch.
	event := AuditEvent{
		UserID:       1,
		Username:     "tester",
		ActionType:   "test.action",
		ResourceType: "test",
		Details:      map[string]interface{}{"bad": make(chan int)},
		Success:      true,
	}

	if err := LogAudit(db, event); err != nil {
		t.Fatalf("LogAudit returned error: %v", err)
	}

	var details *string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action_type = ? AND username = ?`,
		"test.action", "tester").Scan(&details); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	if details == nil {
		t.Fatalf("expected sentinel details, got NULL")
	}
	if !strings.Contains(*details, "details_marshal_error") {
		t.Errorf("expected sentinel marker in details, got %q", *details)
	}
}

// TestLogAudit_DetailsRedaction_RedactsSensitiveKeys verifies that central
// audit detail sanitization removes common credential-bearing values.
func TestLogAudit_DetailsRedaction_RedactsSensitiveKeys(t *testing.T) {
	db, err := database.NewSQLiteDB("file:logger_audit_redaction_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecWrite(`CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		user_id INTEGER,
		username TEXT NOT NULL,
		ip_address TEXT,
		user_agent TEXT,
		action_type TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		resource_id INTEGER,
		resource_name TEXT,
		details TEXT,
		success BOOLEAN NOT NULL DEFAULT TRUE,
		error_message TEXT,
		source TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create audit_logs: %v", err)
	}

	if err := LogAudit(db, AuditEvent{
		UserID:       3,
		Username:     "redact",
		ActionType:   "test.action",
		ResourceType: "test",
		Details: map[string]interface{}{
			"client_secret": "super-secret",
			"nested": map[string]interface{}{
				"access_token": "token-value",
				"safe":         "kept",
			},
		},
		Success: true,
	}); err != nil {
		t.Fatalf("LogAudit returned error: %v", err)
	}

	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE username = ?`, "redact").Scan(&details); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	if strings.Contains(details, "super-secret") || strings.Contains(details, "token-value") {
		t.Fatalf("details leaked sensitive value: %s", details)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(details), &parsed); err != nil {
		t.Fatalf("details JSON invalid: %v", err)
	}
	if parsed["client_secret"] != auditDetailsRedactedValue {
		t.Fatalf("client_secret was not redacted: %#v", parsed["client_secret"])
	}
	nested := parsed["nested"].(map[string]interface{})
	if nested["access_token"] != auditDetailsRedactedValue || nested["safe"] != "kept" {
		t.Fatalf("nested details unexpected: %#v", nested)
	}
}

// TestLogAudit_SourceLabeling covers the structured log's source field. An
// event with no authenticated user (failed login, rejected key, background
// work) must not be reported as SCIM; only scim.* actions or an explicit
// Source are. Regression: login.failure logged source=SCIM because the code
// treated UserID == 0 as a SCIM marker.
func TestLogAudit_SourceLabeling(t *testing.T) {
	tests := []struct {
		name    string
		event   AuditEvent
		want    string
		notWant string
	}{
		{
			name: "unauthenticated login failure is SYSTEM, not SCIM",
			event: AuditEvent{
				UserID:       0,
				Username:     "unknown",
				ActionType:   "login.failure",
				ResourceType: "user",
				Success:      false,
			},
			want:    "source=SYSTEM",
			notWant: "source=SCIM",
		},
		{
			name: "scim action is SCIM",
			event: AuditEvent{
				UserID:       0,
				Username:     "SCIM:abc",
				ActionType:   "scim.user.create",
				ResourceType: "user",
				Success:      true,
			},
			want: "source=SCIM",
		},
		{
			name: "authenticated action is USER",
			event: AuditEvent{
				UserID:       7,
				Username:     "alice",
				ActionType:   "workspace.create",
				ResourceType: "workspace",
				Success:      true,
			},
			want: "source=USER",
		},
		{
			name: "explicit source overrides derivation",
			event: AuditEvent{
				UserID:       0,
				Username:     "scheduler",
				ActionType:   "login.failure",
				ResourceType: "user",
				Source:       "SCHEDULER",
				Success:      false,
			},
			want: "source=SCHEDULER",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			db, err := database.NewSQLiteDB(fmt.Sprintf("file:logger_audit_source_%d?mode=memory&cache=shared", i))
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			if _, err := db.ExecWrite(`CREATE TABLE audit_logs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				user_id INTEGER,
				username TEXT NOT NULL,
				ip_address TEXT,
				user_agent TEXT,
				action_type TEXT NOT NULL,
				resource_type TEXT NOT NULL,
				resource_id INTEGER,
				resource_name TEXT,
				details TEXT,
				success BOOLEAN NOT NULL DEFAULT TRUE,
				error_message TEXT,
				source TEXT NOT NULL DEFAULT ''
			)`); err != nil {
				t.Fatalf("create audit_logs: %v", err)
			}

			_ = LogAudit(db, tt.event)

			logged := buf.String()
			if !strings.Contains(logged, tt.want) {
				t.Errorf("expected %q in log line, got %q", tt.want, logged)
			}
			if tt.notWant != "" && strings.Contains(logged, tt.notWant) {
				t.Errorf("did not expect %q in log line, got %q", tt.notWant, logged)
			}
		})
	}
}

func TestLogAudit_DetailsMarshalSuccess_StoresJSON(t *testing.T) {
	db, err := database.NewSQLiteDB("file:logger_audit_marshal_ok_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecWrite(`CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		user_id INTEGER,
		username TEXT NOT NULL,
		ip_address TEXT,
		user_agent TEXT,
		action_type TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		resource_id INTEGER,
		resource_name TEXT,
		details TEXT,
		success BOOLEAN NOT NULL DEFAULT TRUE,
		error_message TEXT,
		source TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create audit_logs: %v", err)
	}

	if err := LogAudit(db, AuditEvent{
		UserID:       2,
		Username:     "happy",
		ActionType:   "test.action",
		ResourceType: "test",
		Details:      map[string]interface{}{"old": "a", "new": "b"},
		Success:      true,
	}); err != nil {
		t.Fatalf("LogAudit returned error: %v", err)
	}

	var details *string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE username = ?`, "happy").Scan(&details); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	if details == nil {
		t.Fatalf("expected details JSON, got NULL")
	}
	if strings.Contains(*details, "details_marshal_error") {
		t.Errorf("did not expect sentinel marker on happy path, got %q", *details)
	}
	if !strings.Contains(*details, `"old":"a"`) || !strings.Contains(*details, `"new":"b"`) {
		t.Errorf("expected marshaled key/value pairs in details, got %q", *details)
	}
}

func newAuditDetailsTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB("file:logger_audit_details_" + t.Name() + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecWrite(`CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		user_id INTEGER,
		username TEXT NOT NULL,
		ip_address TEXT,
		user_agent TEXT,
		action_type TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		resource_id INTEGER,
		resource_name TEXT,
		details TEXT,
		success BOOLEAN NOT NULL DEFAULT TRUE,
		error_message TEXT,
		source TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create audit_logs: %v", err)
	}
	return db
}

// TestLogAudit_DetailsRedaction_KeepsBooleanFlags verifies that a
// sensitive-looking boolean flag (has_secret) is kept while a real secret
// value is still redacted.
func TestLogAudit_DetailsRedaction_KeepsBooleanFlags(t *testing.T) {
	db := newAuditDetailsTestDB(t)

	if err := LogAudit(db, AuditEvent{
		UserID:       4,
		Username:     "flags",
		ActionType:   "test.action",
		ResourceType: "test",
		Details: map[string]interface{}{
			"has_secret":    true,
			"client_secret": "super-secret",
		},
		Success: true,
	}); err != nil {
		t.Fatalf("LogAudit returned error: %v", err)
	}

	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE username = ?`, "flags").Scan(&details); err != nil {
		t.Fatalf("query audit row: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(details), &parsed); err != nil {
		t.Fatalf("details JSON invalid: %v", err)
	}
	if parsed["has_secret"] != true {
		t.Errorf("has_secret was not preserved: %#v", parsed["has_secret"])
	}
	if parsed["client_secret"] != auditDetailsRedactedValue {
		t.Errorf("client_secret was not redacted: %#v", parsed["client_secret"])
	}
}

// TestLogAudit_DetailsTruncation_PreservesScalars verifies that an oversized
// details payload is shrunk rather than replaced wholesale, so summary
// scalars survive for large operations such as bulk cleanup.
func TestLogAudit_DetailsTruncation_PreservesScalars(t *testing.T) {
	db := newAuditDetailsTestDB(t)

	big := make([]interface{}, 0, 4000)
	for i := 0; i < 4000; i++ {
		big = append(big, map[string]interface{}{
			"id":   i,
			"name": strings.Repeat("x", 64),
		})
	}

	if err := LogAudit(db, AuditEvent{
		UserID:       5,
		Username:     "bulk",
		ActionType:   "portal_customer.bulk_cleanup",
		ResourceType: "portal_customer",
		Details: map[string]interface{}{
			"erased_count":    123,
			"scanned_count":   456,
			"truncated":       true,
			"erased_customers": big,
		},
		Success: true,
	}); err != nil {
		t.Fatalf("LogAudit returned error: %v", err)
	}

	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE username = ?`, "bulk").Scan(&details); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	if len(details) > auditDetailsMaxBytes {
		t.Fatalf("stored details still exceed limit: %d bytes", len(details))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(details), &parsed); err != nil {
		t.Fatalf("details JSON invalid: %v", err)
	}
	if parsed["details_truncated"] != true {
		t.Errorf("expected details_truncated marker, got %#v", parsed["details_truncated"])
	}
	if parsed["erased_count"] != float64(123) || parsed["scanned_count"] != float64(456) {
		t.Errorf("summary scalars were lost: %#v", parsed)
	}
}

// TestLogAuditTx_CommitsAndRollsBackWithTransaction verifies the
// transaction-aware writer shares the sanitization path and that its row is
// rolled back with the surrounding transaction.
func TestLogAuditTx_CommitsAndRollsBackWithTransaction(t *testing.T) {
	db := newAuditDetailsTestDB(t)
	ctx := context.Background()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := LogAuditTx(ctx, tx, AuditEvent{
		UserID:       9,
		Username:     "tx-committed",
		ActionType:   "agent.chat.turn",
		ResourceType: "agent_session",
		Details:      map[string]interface{}{"client_secret": "super-secret"},
		Success:      true,
	}); err != nil {
		t.Fatalf("LogAuditTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE username = ?`, "tx-committed").Scan(&details); err != nil {
		t.Fatalf("query committed row: %v", err)
	}
	if strings.Contains(details, "super-secret") || !strings.Contains(details, auditDetailsRedactedValue) {
		t.Fatalf("transactional audit details were not sanitized: %s", details)
	}

	tx2, err := db.Begin()
	if err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	if err := LogAuditTx(ctx, tx2, AuditEvent{
		UserID:       10,
		Username:     "tx-rolled-back",
		ActionType:   "agent.chat.turn",
		ResourceType: "agent_session",
		Success:      true,
	}); err != nil {
		t.Fatalf("LogAuditTx 2: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE username = ?`, "tx-rolled-back").Scan(&count); err != nil {
		t.Fatalf("count rolled-back row: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back audit rows = %d, want 0", count)
	}
}

// TestLogAudit_PersistsSource verifies the resolved source is stored on the
// row so non-HTTP events can be filtered exactly.
func TestLogAudit_PersistsSource(t *testing.T) {
	db := newAuditDetailsTestDB(t)

	events := []AuditEvent{
		{UserID: 1, Username: "alice", ActionType: "workspace.create", ResourceType: "workspace", Success: true},
		{UserID: 0, Username: "system:ldap", Source: "SYSTEM", ActionType: "ldap.sync", ResourceType: "ldap_config", Success: true},
		{UserID: 0, Username: "SCIM:abc", ActionType: "scim.user.create", ResourceType: "user", Success: true},
	}
	for _, event := range events {
		if err := LogAudit(db, event); err != nil {
			t.Fatalf("LogAudit(%s): %v", event.Username, err)
		}
	}

	rows, err := db.Query(`SELECT username, source FROM audit_logs ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var username, source string
		if err := rows.Scan(&username, &source); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[username] = source
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	want := map[string]string{"alice": "USER", "system:ldap": "SYSTEM", "SCIM:abc": "SCIM"}
	for username, source := range want {
		if got[username] != source {
			t.Errorf("source for %q = %q, want %q", username, got[username], source)
		}
	}
}

// TestAuditorLogDenied_RecordsPermissionAndRoute covers the denial contract
// consumed by the middleware and handler helpers (WI-1888).
func TestAuditorLogDenied_RecordsPermissionAndRoute(t *testing.T) {
	db := newAuditDetailsTestDB(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v2/admin/users/7", nil)
	user := &models.User{ID: 3, Username: "mallory"}

	NewAuditor(db).LogDenied(r, user, "access.denied", "route", nil, r.URL.Path, "system.admin", nil)

	var success bool
	var errorMessage, details string
	if err := db.QueryRow(`SELECT success, error_message, details FROM audit_logs WHERE username = ?`, "mallory").Scan(&success, &errorMessage, &details); err != nil {
		t.Fatalf("query denial row: %v", err)
	}
	if success {
		t.Errorf("denial row success = true, want false")
	}
	if errorMessage != "permission_denied" {
		t.Errorf("error_message = %q, want permission_denied", errorMessage)
	}
	for _, want := range []string{"required_permission", "system.admin", "route", "POST"} {
		if !strings.Contains(details, want) {
			t.Errorf("details %q missing %q", details, want)
		}
	}
}
