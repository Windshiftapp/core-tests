package aitools

import (
	"encoding/json"
	"testing"

	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/testutils"
)

// newAuditTestDB creates an isolated initialized test DB plus a single user
// row, returning the DB handle and the user's allocated row ID. audit_logs has
// a FK on users(id), so a real user must exist before any audit row can land.
func newAuditTestDB(t *testing.T, fullName string) (database.Database, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	db := tdb.DB
	var uid int64
	err := db.QueryRow(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, fullName, fullName+"@example.test", fullName).Scan(&uid)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return db, int(uid)
}

// TestEmitActionAudit_WritesRow ensures that agent-driven action mutations
// land an audit row distinguishable from cookie-auth writes via the
// `details.source` field. The HTTP handler already audits these mutations;
// this test pins the equivalent behavior for the chat / MCP path.
func TestEmitActionAudit_WritesRow(t *testing.T) {
	db, userID := newAuditTestDB(t, "Alice")

	env := &Env{
		DB:       db,
		UserID:   userID,
		Username: "Alice",
		Source:   SourceAIChat,
	}

	emitActionAudit(env, logger.ActionAutomationCreate, 7, 123, "My Automation")

	// Read back the row.
	var (
		gotUserID       int
		gotUsername     string
		gotActionType   string
		gotResourceType string
		gotResourceID   int
		gotResourceName string
		gotDetails      string
		gotSuccess      bool
	)
	row := db.QueryRow(`
		SELECT user_id, username, action_type, resource_type, resource_id,
		       resource_name, COALESCE(details, ''), success
		FROM audit_logs
		WHERE action_type = ?
		ORDER BY id DESC LIMIT 1
	`, logger.ActionAutomationCreate)
	if err := row.Scan(&gotUserID, &gotUsername, &gotActionType, &gotResourceType,
		&gotResourceID, &gotResourceName, &gotDetails, &gotSuccess); err != nil {
		t.Fatalf("query audit_logs: %v", err)
	}

	if gotUserID != env.UserID {
		t.Errorf("user_id: want %d, got %d", env.UserID, gotUserID)
	}
	if gotUsername != "Alice" {
		t.Errorf("username: want Alice, got %q", gotUsername)
	}
	if gotResourceType != logger.ResourceAutomation {
		t.Errorf("resource_type: want %q, got %q", logger.ResourceAutomation, gotResourceType)
	}
	if gotResourceID != 123 {
		t.Errorf("resource_id: want 123, got %d", gotResourceID)
	}
	if gotResourceName != "My Automation" {
		t.Errorf("resource_name: want %q, got %q", "My Automation", gotResourceName)
	}
	if !gotSuccess {
		t.Errorf("success: want true, got false")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(gotDetails), &parsed); err != nil {
		t.Fatalf("unmarshal details: %v (raw: %s)", err, gotDetails)
	}
	if parsed["source"] != SourceAIChat {
		t.Errorf("details.source: want %q, got %v", SourceAIChat, parsed["source"])
	}
	// workspace_id round-trips through JSON as a float64.
	if ws, ok := parsed["workspace_id"].(float64); !ok || int(ws) != 7 {
		t.Errorf("details.workspace_id: want 7, got %v", parsed["workspace_id"])
	}
}

// TestAuditWrite_WritesRow pins the generic write-audit helper every mutating
// aitool calls (WI-357): the tool name lands as the action type, the entity
// type/id as the resource, and details.source tags the initiating surface.
func TestAuditWrite_WritesRow(t *testing.T) {
	db, userID := newAuditTestDB(t, "Carol")

	env := &Env{DB: db, UserID: userID, Username: "Carol", Source: SourceMCP}
	env.AuditWrite(logger.ResourceItem, 55, "update_item", "Fix the flux capacitor")

	var (
		gotUserID       int
		gotActionType   string
		gotResourceType string
		gotResourceID   int
		gotResourceName string
		gotDetails      string
		gotSuccess      bool
	)
	row := db.QueryRow(`
		SELECT user_id, action_type, resource_type, resource_id,
		       resource_name, COALESCE(details, ''), success
		FROM audit_logs
		WHERE action_type = 'update_item'
		ORDER BY id DESC LIMIT 1
	`)
	if err := row.Scan(&gotUserID, &gotActionType, &gotResourceType,
		&gotResourceID, &gotResourceName, &gotDetails, &gotSuccess); err != nil {
		t.Fatalf("query audit_logs: %v", err)
	}

	if gotUserID != userID {
		t.Errorf("user_id: want %d, got %d", userID, gotUserID)
	}
	if gotResourceType != logger.ResourceItem {
		t.Errorf("resource_type: want %q, got %q", logger.ResourceItem, gotResourceType)
	}
	if gotResourceID != 55 {
		t.Errorf("resource_id: want 55, got %d", gotResourceID)
	}
	if gotResourceName != "Fix the flux capacitor" {
		t.Errorf("resource_name: want %q, got %q", "Fix the flux capacitor", gotResourceName)
	}
	if !gotSuccess {
		t.Errorf("success: want true, got false")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(gotDetails), &parsed); err != nil {
		t.Fatalf("unmarshal details: %v (raw: %s)", err, gotDetails)
	}
	if parsed["source"] != SourceMCP {
		t.Errorf("details.source: want %q, got %v", SourceMCP, parsed["source"])
	}
}

func TestAuditWrite_StandardAgentAddsCorrelationWithoutToolPayloads(t *testing.T) {
	db, userID := newAuditTestDB(t, "StudioAgent")
	env := &Env{
		DB:       db,
		UserID:   userID,
		Username: "StudioAgent",
		Source:   SourceStandardAgent,
		AuditDetails: map[string]interface{}{
			"agent_run_id":              71,
			"agent_profile_id":          9,
			"root_initiator_user_id":    12,
			"immediate_trigger_user_id": 13,
			"acting_user_id":            userID,
			"workspace_id":              2,
			"item_id":                   848,
		},
	}
	env.AuditWrite(logger.ResourceItem, 848, "update_item", "Agent update")

	var details string
	if err := db.QueryRow(`
		SELECT details FROM audit_logs
		WHERE action_type = 'update_item'
		ORDER BY id DESC LIMIT 1
	`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(details), &parsed); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if parsed["source"] != SourceStandardAgent ||
		int(parsed["agent_run_id"].(float64)) != 71 ||
		int(parsed["root_initiator_user_id"].(float64)) != 12 {
		t.Fatalf("missing Standard correlation: %v", parsed)
	}
	for _, forbidden := range []string{"arguments", "result", "tool_arguments", "tool_result"} {
		if _, ok := parsed[forbidden]; ok {
			t.Fatalf("audit details persisted forbidden %q payload: %v", forbidden, parsed)
		}
	}
}

// TestEmitActionAudit_MCPSource sanity-checks that the MCP adapter's Source
// constant is recorded distinctly from the chat path.
func TestEmitActionAudit_MCPSource(t *testing.T) {
	db, userID := newAuditTestDB(t, "Bob")

	env := &Env{DB: db, UserID: userID, Username: "Bob", Source: SourceMCP}
	emitActionAudit(env, logger.ActionAutomationUpdate, 1, 9, "x")

	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action_type = ? ORDER BY id DESC LIMIT 1`,
		logger.ActionAutomationUpdate).Scan(&details); err != nil {
		t.Fatalf("query: %v", err)
	}
	var parsed map[string]interface{}
	_ = json.Unmarshal([]byte(details), &parsed)
	if parsed["source"] != SourceMCP {
		t.Errorf("details.source: want %q, got %v", SourceMCP, parsed["source"])
	}
}
