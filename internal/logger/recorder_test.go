//go:build test

package logger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
)

// TestAuditRecorder_FlushWritesActorAndGatesSuccess covers the WI-1898
// recorder contract: the middleware-installed recorder flushes once, every
// persisted row carries the authenticated actor, and a success event recorded
// before a later failure is dropped while the explicit failure event survives.
func TestAuditRecorder_FlushWritesActorAndGatesSuccess(t *testing.T) {
	db := newAuditDetailsTestDB(t)
	auditor := NewAuditor(db)

	request := httptest.NewRequest(http.MethodPatch, "/api/v2/admin/users/42", nil)
	request.RemoteAddr = "203.0.113.7:5555"
	request.Header.Set("User-Agent", "recorder-test")
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{
		ID:       42,
		Username: "alice",
	}))

	recorder := NewAuditRecorder()
	request = request.WithContext(WithAuditRecorder(request.Context(), recorder))

	resourceID := 7
	Record(request, "user.update", "user", &resourceID, "alice", map[string]any{"field": "timezone"})
	RecordFailure(request, "user.update", "user", &resourceID, "alice", "conflict", nil)

	// The handler failed, so the success event must not be persisted.
	auditor.Flush(recorder, false)

	rows, err := db.Query(`SELECT username, ip_address, user_agent, action_type, success, error_message
		FROM audit_logs WHERE resource_type = ? ORDER BY id`, "user")
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type row struct {
		username, ip, userAgent, action, errorMessage string
		success                                       bool
	}
	var got []row
	for rows.Next() {
		var current row
		if err := rows.Scan(&current.username, &current.ip, &current.userAgent, &current.action, &current.success, &current.errorMessage); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		got = append(got, current)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected only the failure event to persist, got %#v", got)
	}
	failure := got[0]
	if failure.username != "alice" || failure.ip != "203.0.113.7" || failure.userAgent != "recorder-test" {
		t.Errorf("failure event actor not populated: %#v", failure)
	}
	if failure.success || failure.errorMessage != "conflict" {
		t.Errorf("failure event shape unexpected: %#v", failure)
	}

	// Flushing again must not re-write the drained events.
	auditor.Flush(recorder, true)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Errorf("second flush wrote duplicate rows: count=%d", count)
	}
}

// TestAuditRecorder_SuccessPersistsOn2xx verifies the positive path: a success
// event flushes when the handler succeeded.
func TestAuditRecorder_SuccessPersistsOn2xx(t *testing.T) {
	db := newAuditDetailsTestDB(t)
	auditor := NewAuditor(db)

	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/groups", nil)
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{
		ID:       9,
		Username: "bob",
	}))
	recorder := NewAuditRecorder()
	request = request.WithContext(WithAuditRecorder(request.Context(), recorder))

	Record(request, "group.create", "group", nil, "engineers", nil)
	auditor.Flush(recorder, true)

	var username string
	if err := db.QueryRow(`SELECT username FROM audit_logs WHERE action_type = ?`, "group.create").Scan(&username); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	if username != "bob" {
		t.Errorf("actor = %q, want bob", username)
	}
}

// TestAuditRecorder_AbsentIsNoOp verifies handlers can call Record without a
// recorder installed (unit tests, non-HTTP callers) without panicking.
func TestAuditRecorder_AbsentIsNoOp(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/groups", nil)
	Record(request, "group.create", "group", nil, "engineers", nil)
	RecordDenied(request, "access.denied", "route", nil, "/api/v2/admin/groups", "system.admin", nil)
}
