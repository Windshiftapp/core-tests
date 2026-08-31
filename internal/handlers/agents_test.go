package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
)

func TestUpdateOwnedAgentName(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "agents.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertUser := func(username string) *models.User {
		t.Helper()
		result, err := db.ExecWrite(`
			INSERT INTO users (email, username, first_name, last_name, is_active)
			VALUES (?, ?, ?, 'User', true)
		`, username+"@example.test", username, username)
		if err != nil {
			t.Fatalf("insert user: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return &models.User{ID: int(id), Username: username, Email: username + "@example.test"}
	}
	insertAgent := func(ownerID int, username string) int {
		t.Helper()
		result, err := db.ExecWrite(`
			INSERT INTO users (
				email, username, first_name, last_name, is_active, is_agent,
				agent_owner_user_id, agent_provenance
			) VALUES (?, ?, 'Old', 'Name', true, true, ?, 'user')
		`, username+"@agents.test", username, ownerID)
		if err != nil {
			t.Fatalf("insert agent: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return int(id)
	}
	request := func(agentID int, body string, user *models.User) *http.Request {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/me/agents/%d", agentID), strings.NewReader(body))
		req.SetPathValue("id", fmt.Sprintf("%d", agentID))
		return req.WithContext(context.WithValue(req.Context(), contextkeys.User, user))
	}

	owner := insertUser("owner")
	otherOwner := insertUser("other-owner")
	agentID := insertAgent(owner.ID, "ws-cli-machine")
	handler := &AgentHandler{db: db}

	recorder := httptest.NewRecorder()
	handler.Update(recorder, request(agentID, `{"name":"Build Agent"}`, owner))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var updated models.User
	if err := json.NewDecoder(recorder.Body).Decode(&updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.FullName != "Build Agent" || updated.Username != "ws-cli-machine" {
		t.Fatalf("updated agent = name %q username %q", updated.FullName, updated.Username)
	}
	var firstName, lastName, username string
	if err := db.QueryRow(`SELECT first_name, last_name, username FROM users WHERE id = ?`, agentID).Scan(&firstName, &lastName, &username); err != nil {
		t.Fatalf("load updated agent: %v", err)
	}
	if firstName != "Build Agent" || lastName != "" || username != "ws-cli-machine" {
		t.Fatalf("stored agent = first_name %q last_name %q username %q", firstName, lastName, username)
	}

	recorder = httptest.NewRecorder()
	handler.Update(recorder, request(agentID, `{"name":"Stolen"}`, otherOwner))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("non-owner update status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.Update(recorder, request(agentID, `{"name":"   "}`, owner))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("blank name status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}
