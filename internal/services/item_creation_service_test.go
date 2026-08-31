//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

type recordingItemCreatedEmitter struct {
	items         []*models.Item
	actorUserID   int
	actorUsername string
}

func TestItemCreationServiceValidatesAssignee(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var inactiveUserID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('inactive-assignee@example.test', 'inactive-assignee', 'Inactive', 'Assignee', false)
		RETURNING id
	`).Scan(&inactiveUserID); err != nil {
		t.Fatalf("create inactive user: %v", err)
	}
	var deniedUserID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('denied-assignee@example.test', 'denied-assignee', 'Denied', 'Assignee', true)
		RETURNING id
	`).Scan(&deniedUserID); err != nil {
		t.Fatalf("create denied user: %v", err)
	}
	var unboundAgentID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active, is_agent)
		VALUES ('unbound-assignee@agents.test', 'unbound-assignee', 'Unbound', 'Agent', true, true)
		RETURNING id
	`).Scan(&unboundAgentID); err != nil {
		t.Fatalf("create unbound agent: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		VALUES (?, ?, (SELECT id FROM workspace_roles WHERE name = 'Viewer'))
		ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING
	`, data.UserID, data.WorkspaceID); err != nil {
		t.Fatalf("restrict workspace to actor: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		VALUES (?, ?, (SELECT id FROM workspace_roles WHERE name = 'Viewer'))
	`, unboundAgentID, data.WorkspaceID); err != nil {
		t.Fatalf("grant unbound agent workspace access: %v", err)
	}

	var unknownUserID int
	if err := tdb.QueryRow(`SELECT COALESCE(MAX(id), 0) + 1000 FROM users`).Scan(&unknownUserID); err != nil {
		t.Fatalf("choose unknown user ID: %v", err)
	}

	perm, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	service := NewItemCreationService(tdb.GetDatabase(), perm)

	tests := []struct {
		name       string
		assigneeID int
		wantError  bool
	}{
		{name: "active user", assigneeID: data.UserID},
		{name: "active user without workspace access", assigneeID: deniedUserID, wantError: true},
		{name: "agent without ready binding", assigneeID: unboundAgentID, wantError: true},
		{name: "inactive user", assigneeID: inactiveUserID, wantError: true},
		{name: "unknown user", assigneeID: unknownUserID, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := "Assignee validation: " + tt.name
			result, createErr := service.Create(data.UserID, "testuser", ItemCreateInput{
				WorkspaceID: data.WorkspaceID,
				Title:       title,
				AssigneeID:  &tt.assigneeID,
			})

			if !tt.wantError {
				if createErr != nil {
					t.Fatalf("create item: %v", createErr)
				}
				if result.Item.AssigneeID == nil || *result.Item.AssigneeID != tt.assigneeID {
					t.Fatalf("created assignee = %v, want %d", result.Item.AssigneeID, tt.assigneeID)
				}
				return
			}

			var validationErr *validation.ValidationError
			if !errors.As(createErr, &validationErr) {
				t.Fatalf("create error = %v, want ValidationError", createErr)
			}
			if validationErr.Field != "assignee_id" || validationErr.Message != "Assignee user not found" {
				t.Fatalf("validation error = %#v, want assignee_id/Assignee user not found", validationErr)
			}

			var count int
			if err := tdb.QueryRow(`SELECT COUNT(*) FROM items WHERE title = ?`, title).Scan(&count); err != nil {
				t.Fatalf("count persisted items: %v", err)
			}
			if count != 0 {
				t.Fatalf("persisted items = %d, want 0", count)
			}
		})
	}
}

func (e *recordingItemCreatedEmitter) EmitItemCreated(item *models.Item, actorUserID int, actorUsername ...string) {
	e.items = append(e.items, item)
	e.actorUserID = actorUserID
	if len(actorUsername) > 0 {
		e.actorUsername = actorUsername[0]
	}
}

func TestItemCreationService_PreservesSourcePersistsAndEmitsCommittedItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var parentTypeID, childTypeID int
	if err := tdb.QueryRow(`
		SELECT id FROM item_types
		WHERE hierarchy_level >= 0
		ORDER BY hierarchy_level, id LIMIT 1
	`).Scan(&parentTypeID); err != nil {
		t.Fatalf("load parent item type: %v", err)
	}
	if err := tdb.QueryRow(`
		SELECT id FROM item_types
		WHERE hierarchy_level > (SELECT hierarchy_level FROM item_types WHERE id = ?)
		ORDER BY hierarchy_level, id LIMIT 1
	`, parentTypeID).Scan(&childTypeID); err != nil {
		t.Fatalf("load child item type: %v", err)
	}

	emitter := &recordingItemCreatedEmitter{}
	perm, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	service := NewItemCreationService(tdb.GetDatabase(), perm)
	service.SetEmitter(emitter)

	parent, err := service.Create(data.UserID, "testuser", ItemCreateInput{
		WorkspaceID: data.WorkspaceID,
		ItemTypeID:  &parentTypeID,
		Title:       "Parent",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	child, err := service.Create(data.UserID, "testuser", ItemCreateInput{
		WorkspaceID: data.WorkspaceID,
		ItemTypeID:  &childTypeID,
		ParentID:    &parent.Item.ID,
		Title:       "<script>bad()</script>Child",
		Description: "before<script>bad()</script><br/>after",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.Item.Title != "<script>bad()</script>Child" || child.Item.Description != "before<script>bad()</script><br/>after" {
		t.Fatalf("created child source changed: %+v", child.Item)
	}

	var parentID *int
	var inheritProject bool
	if err := tdb.QueryRow(`SELECT parent_id, inherit_project FROM items WHERE id = ?`, child.Item.ID).Scan(&parentID, &inheritProject); err != nil {
		t.Fatalf("load persisted child: %v", err)
	}
	if parentID == nil || *parentID != parent.Item.ID || !inheritProject {
		t.Fatalf("persisted parent/inherit_project = %v/%v, want %d/true", parentID, inheritProject, parent.Item.ID)
	}

	if len(emitter.items) != 2 || emitter.items[1].ID != child.Item.ID || emitter.actorUserID != data.UserID || emitter.actorUsername != "testuser" {
		t.Fatalf("created-item events = count %d item %+v actor %d/%q", len(emitter.items), emitter.items, emitter.actorUserID, emitter.actorUsername)
	}

	beforeDenied := len(emitter.items)
	_, err = service.Create(data.UserID, "testuser", ItemCreateInput{
		WorkspaceID: data.WorkspaceID,
		ItemTypeID:  &parentTypeID,
		Title:       "   ",
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "title" || validationErr.Message != "Title is required" {
		t.Fatalf("invalid title error = %v, want title ValidationError", err)
	}
	if len(emitter.items) != beforeDenied {
		t.Fatalf("validation failure emitted an item-created event")
	}
}
