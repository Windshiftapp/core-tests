package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils/factory"
)

func TestCommentAgentOwnerAttributionPreservesUserListVisibility(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "comment-attribution.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId for %s: %v", label, err)
		}
		return int(id)
	}

	ownerID := insertID("owner", `INSERT INTO users (email, username, first_name, last_name) VALUES ('owner@example.test', 'owner', 'Agent', 'Owner')`)
	agentID := insertID("agent", `INSERT INTO users (email, username, first_name, last_name, is_agent, agent_owner_user_id) VALUES ('agent@example.test', 'agent', 'Comment', 'Agent', true, ?)`, ownerID)
	viewerID := insertID("viewer", `INSERT INTO users (email, username, first_name, last_name) VALUES ('viewer@example.test', 'viewer', 'Plain', 'Viewer')`)
	directoryViewerID := insertID("directory viewer", `INSERT INTO users (email, username, first_name, last_name) VALUES ('directory@example.test', 'directory', 'Directory', 'Viewer')`)
	workspaceID := insertID("workspace", `INSERT INTO workspaces (name, key) VALUES ('Comment Attribution', 'CAT')`)
	f := factory.NewTestFactory(db)
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Attributed item",
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	insertID("comment", `INSERT INTO comments (item_id, author_id, content) VALUES (?, ?, 'Agent comment')`, itemID, agentID)

	for _, userID := range []int{viewerID, directoryViewerID} {
		if _, err := db.ExecWrite(`
			INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
			SELECT ?, ?, id FROM workspace_roles WHERE name = 'Viewer'
		`, userID, workspaceID); err != nil {
			t.Fatalf("grant viewer role: %v", err)
		}
	}
	if _, err := db.ExecWrite(`
		INSERT INTO user_global_permissions (user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = 'user.list'
	`, directoryViewerID); err != nil {
		t.Fatalf("grant user.list: %v", err)
	}

	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: 0, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	handler := NewCommentHandler(db, permissionService, nil, nil)
	requestFor := func(userID int) *http.Request {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items/%d/comments", itemID), nil)
		request.SetPathValue("id", fmt.Sprintf("%d", itemID))
		return request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: userID}))
	}
	loadFor := func(userID int) []models.Comment {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.GetComments(recorder, requestFor(userID))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status for user %d = %d, want 200; body=%s", userID, recorder.Code, recorder.Body.String())
		}
		var response struct {
			Comments []models.Comment `json:"comments"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode comments: %v", err)
		}
		return response.Comments
	}

	plainComments := loadFor(viewerID)
	if len(plainComments) != 1 || plainComments[0].AgentOwnerName != "" {
		t.Fatalf("plain viewer comments = %+v, want owner attribution omitted", plainComments)
	}
	directoryComments := loadFor(directoryViewerID)
	if len(directoryComments) != 1 || directoryComments[0].AgentOwnerName != "Agent Owner" {
		t.Fatalf("directory viewer comments = %+v, want owner attribution", directoryComments)
	}
}

func TestFilterPageRevisionAuthorsMatchesUserProfileVisibility(t *testing.T) {
	revisions := func() []models.PageRevision {
		return []models.PageRevision{
			{CreatedBy: 1, Author: &models.PageRevisionAuthor{ID: 1, Name: "Self", IsActive: false}},
			{CreatedBy: 2, Author: &models.PageRevisionAuthor{ID: 2, Name: "Active", IsActive: true}},
			{CreatedBy: 3, Author: &models.PageRevisionAuthor{ID: 3, Name: "Inactive", IsActive: false}},
		}
	}

	withoutDirectory := revisions()
	filterPageRevisionAuthors(withoutDirectory, 1, false, false)
	if withoutDirectory[0].Author == nil || withoutDirectory[1].Author != nil || withoutDirectory[2].Author != nil {
		t.Fatalf("no-directory visibility = %+v", withoutDirectory)
	}

	withDirectory := revisions()
	filterPageRevisionAuthors(withDirectory, 1, false, true)
	if withDirectory[0].Author == nil || withDirectory[1].Author == nil || withDirectory[2].Author != nil {
		t.Fatalf("directory visibility = %+v", withDirectory)
	}

	admin := revisions()
	filterPageRevisionAuthors(admin, 1, true, false)
	for i := range admin {
		if admin[i].Author == nil {
			t.Fatalf("admin revision %d lost author: %+v", i, admin)
		}
	}
}
