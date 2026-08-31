//go:build test

package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/testutils"
)

func TestPageApplicationService_MoveCrossWorkspaceMovesSubtreeAndRehomesWorkspaceData(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var destinationWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Destination Workspace', 'DEST', 'Cross-workspace page destination', true)
		RETURNING id
	`).Scan(&destinationWorkspaceID); err != nil {
		t.Fatalf("create destination workspace: %v", err)
	}
	var moverID int
	if err := tdb.QueryRow(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, is_active)
		VALUES ('page_subtree_mover', 'page_subtree_mover@test.com', 'Page', 'Mover', 'hash', true)
		RETURNING id
	`).Scan(&moverID); err != nil {
		t.Fatalf("create page mover: %v", err)
	}
	var adminRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Administrator'`).Scan(&adminRoleID); err != nil {
		t.Fatalf("load Administrator role: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, moverID, data.WorkspaceID, adminRoleID); err != nil {
		t.Fatalf("grant source Administrator role: %v", err)
	}
	var viewerRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Viewer'`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load Viewer role: %v", err)
	}
	// An explicit Viewer assignment closes the destination workspace's
	// implicit Editor access, so the mover genuinely lacks page.create.
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, data.UserID, destinationWorkspaceID, viewerRoleID); err != nil {
		t.Fatalf("restrict destination workspace to explicit viewers: %v", err)
	}

	pageService := NewPageService(tdb.GetDatabase())
	root, err := pageService.Create(moverID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Source root",
		Content:     "root body",
		IsHome:      true,
	})
	if err != nil {
		t.Fatalf("create source root: %v", err)
	}
	child, err := pageService.Create(moverID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &root.ID,
		Title:       "Source child",
		Content:     "child body",
	})
	if err != nil {
		t.Fatalf("create source child: %v", err)
	}
	destinationParent, err := pageService.Create(moverID, CreatePageInput{
		WorkspaceID: destinationWorkspaceID,
		Title:       "Destination parent",
	})
	if err != nil {
		t.Fatalf("create destination parent: %v", err)
	}

	newApplication := func() *PageApplicationService {
		permissions, permissionErr := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
		if permissionErr != nil {
			t.Fatalf("create permission service: %v", permissionErr)
		}
		return NewPageApplicationService(pageService, NewPagePermissionService(tdb.GetDatabase(), permissions))
	}
	actor := AuditActor{UserID: moverID, Username: "page_subtree_mover", Source: "mcp"}
	destination := destinationWorkspaceID
	_, err = newApplication().Move(actor, data.WorkspaceID, root.ID, &destination, &destinationParent.ID, nil, nil)
	if !errors.Is(err, ErrPageMutationForbidden) {
		t.Fatalf("move without destination create permission = %v, want ErrPageMutationForbidden", err)
	}
	var unchangedWorkspaceID int
	if err := tdb.QueryRow(`SELECT workspace_id FROM pages WHERE id = ?`, root.ID).Scan(&unchangedWorkspaceID); err != nil {
		t.Fatalf("load root after denied move: %v", err)
	}
	if unchangedWorkspaceID != data.WorkspaceID {
		t.Fatalf("denied move changed workspace to %d, want %d", unchangedWorkspaceID, data.WorkspaceID)
	}

	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, moverID, destinationWorkspaceID, adminRoleID); err != nil {
		t.Fatalf("grant destination Administrator role: %v", err)
	}

	var sourceKeepLabelID, sourceDropLabelID, destinationKeepLabelID int
	for _, label := range []struct {
		workspaceID int
		name        string
		id          *int
	}{
		{data.WorkspaceID, "Keep", &sourceKeepLabelID},
		{data.WorkspaceID, "Drop", &sourceDropLabelID},
		{destinationWorkspaceID, "Keep", &destinationKeepLabelID},
	} {
		if err := tdb.QueryRow(`
			INSERT INTO page_labels (workspace_id, name, color)
			VALUES (?, ?, '#336699') RETURNING id
		`, label.workspaceID, label.name).Scan(label.id); err != nil {
			t.Fatalf("create page label %q: %v", label.name, err)
		}
	}
	for _, assignment := range [][2]int{{root.ID, sourceKeepLabelID}, {child.ID, sourceDropLabelID}} {
		if _, err := tdb.Exec(`INSERT INTO page_label_assignments (page_id, page_label_id) VALUES (?, ?)`, assignment[0], assignment[1]); err != nil {
			t.Fatalf("assign source label: %v", err)
		}
	}
	if _, err := tdb.Exec(`
		INSERT INTO page_permissions (page_id, principal_type, principal_id, permission_level, granted_by)
		VALUES (?, 'user', ?, 'edit', ?), (?, 'user', ?, 'view', ?)
	`, root.ID, moverID, moverID, child.ID, moverID, moverID); err != nil {
		t.Fatalf("seed page permissions: %v", err)
	}
	if _, err := tdb.Exec(`UPDATE pages SET inherit_permissions = false WHERE id IN (?, ?)`, root.ID, child.ID); err != nil {
		t.Fatalf("break source inheritance: %v", err)
	}
	var linkTypeID int
	if err := tdb.QueryRow(`SELECT id FROM link_types WHERE active = true ORDER BY id LIMIT 1`).Scan(&linkTypeID); err != nil {
		t.Fatalf("load link type: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, created_by)
		VALUES (?, 'page', ?, 'page', ?, ?)
	`, linkTypeID, root.ID, destinationParent.ID, moverID); err != nil {
		t.Fatalf("seed page link: %v", err)
	}
	var skillID int
	if err := tdb.QueryRow(`
		INSERT INTO workspace_agent_skills (workspace_id, name, description, body, created_by_user_id)
		VALUES (?, 'source-skill', '', '', ?) RETURNING id
	`, data.WorkspaceID, moverID).Scan(&skillID); err != nil {
		t.Fatalf("seed workspace skill: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO workspace_agent_skill_pages (skill_id, page_id) VALUES (?, ?)`, skillID, child.ID); err != nil {
		t.Fatalf("seed workspace skill page: %v", err)
	}

	moved, err := newApplication().Move(actor, data.WorkspaceID, root.ID, &destination, &destinationParent.ID, nil, nil)
	if err != nil {
		t.Fatalf("move subtree across workspaces: %v", err)
	}
	if moved.WorkspaceID != destinationWorkspaceID || moved.ParentID == nil || *moved.ParentID != destinationParent.ID {
		t.Fatalf("moved root workspace/parent = %d/%v, want %d/%d", moved.WorkspaceID, moved.ParentID, destinationWorkspaceID, destinationParent.ID)
	}
	if moved.IsHome {
		t.Fatal("moved root retained is_home")
	}

	wantPath := fmt.Sprintf("/%d/", destinationParent.ID)
	if moved.Path != wantPath || moved.Depth != 1 {
		t.Fatalf("moved root path/depth = %q/%d, want %q/1", moved.Path, moved.Depth, wantPath)
	}
	var childWorkspaceID, childDepth int
	var childPath string
	var childParentID int
	var childInherits bool
	if err := tdb.QueryRow(`
		SELECT workspace_id, parent_id, path, depth, inherit_permissions
		FROM pages WHERE id = ?
	`, child.ID).Scan(&childWorkspaceID, &childParentID, &childPath, &childDepth, &childInherits); err != nil {
		t.Fatalf("load moved child: %v", err)
	}
	wantChildPath := fmt.Sprintf("/%d/%d/", destinationParent.ID, root.ID)
	if childWorkspaceID != destinationWorkspaceID || childParentID != root.ID || childPath != wantChildPath || childDepth != 2 || !childInherits {
		t.Fatalf("moved child = workspace %d parent %d path %q depth %d inherits %v", childWorkspaceID, childParentID, childPath, childDepth, childInherits)
	}

	for name, query := range map[string]string{
		"page permissions":           `SELECT COUNT(*) FROM page_permissions WHERE page_id IN (?, ?)`,
		"cross-workspace links":      `SELECT COUNT(*) FROM item_links WHERE (source_type = 'page' AND source_id IN (?, ?)) OR (target_type = 'page' AND target_id IN (?, ?))`,
		"workspace skill references": `SELECT COUNT(*) FROM workspace_agent_skill_pages WHERE page_id IN (?, ?)`,
	} {
		args := []interface{}{root.ID, child.ID}
		if name == "cross-workspace links" {
			args = append(args, root.ID, child.ID)
		}
		var count int
		if err := tdb.QueryRow(query, args...).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s remaining = %d, want 0", name, count)
		}
	}

	var assignedLabelID int
	if err := tdb.QueryRow(`SELECT page_label_id FROM page_label_assignments WHERE page_id = ?`, root.ID).Scan(&assignedLabelID); err != nil {
		t.Fatalf("load remapped label: %v", err)
	}
	if assignedLabelID != destinationKeepLabelID {
		t.Fatalf("remapped label id = %d, want destination label %d", assignedLabelID, destinationKeepLabelID)
	}
	var droppedLabelCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM page_label_assignments WHERE page_id = ?`, child.ID).Scan(&droppedLabelCount); err != nil {
		t.Fatalf("count dropped labels: %v", err)
	}
	if droppedLabelCount != 0 {
		t.Fatalf("unmatched child labels remaining = %d, want 0", droppedLabelCount)
	}
	var wrongChunkWorkspaceCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM page_chunks WHERE page_id IN (?, ?) AND workspace_id != ?`, root.ID, child.ID, destinationWorkspaceID).Scan(&wrongChunkWorkspaceCount); err != nil {
		t.Fatalf("count stale chunk workspaces: %v", err)
	}
	if wrongChunkWorkspaceCount != 0 {
		t.Fatalf("chunks left in source workspace = %d, want 0", wrongChunkWorkspaceCount)
	}

	var moveAuditCount int
	var detailsJSON string
	if err := tdb.QueryRow(`
		SELECT COUNT(*), MAX(details)
		FROM audit_logs
		WHERE action_type = ? AND resource_type = ? AND resource_id = ?
	`, logger.ActionPageMove, logger.ResourcePage, root.ID).Scan(&moveAuditCount, &detailsJSON); err != nil {
		t.Fatalf("load move audit: %v", err)
	}
	if moveAuditCount != 1 {
		t.Fatalf("move audit count = %d, want 1", moveAuditCount)
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("decode move audit details: %v", err)
	}
	if details["source_workspace_id"] != float64(data.WorkspaceID) || details["destination_workspace_id"] != float64(destinationWorkspaceID) {
		t.Fatalf("move audit workspace details = %#v", details)
	}
}

func TestPageApplicationService_DenialDoesNotMutateOrAuditAndSuccessAuditsOnce(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	perm, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	pageService := NewPageService(tdb.GetDatabase())
	application := NewPageApplicationService(pageService, NewPagePermissionService(tdb.GetDatabase(), perm))

	var viewerID int
	if err := tdb.QueryRow(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, is_active)
		VALUES ('page_app_viewer', 'page_app_viewer@test.com', 'Page', 'Viewer', 'hash', true)
		RETURNING id
	`).Scan(&viewerID); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	var viewerRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Viewer'`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load Viewer role: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, viewerID, data.WorkspaceID, viewerRoleID); err != nil {
		t.Fatalf("assign Viewer role: %v", err)
	}

	_, err = application.Create(AuditActor{UserID: viewerID, Username: "page_app_viewer", Source: "mcp"}, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Denied page",
	})
	if !errors.Is(err, ErrPageMutationForbidden) {
		t.Fatalf("viewer create error = %v, want ErrPageMutationForbidden", err)
	}
	var pageCount, auditCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM pages WHERE workspace_id = ?`, data.WorkspaceID).Scan(&pageCount); err != nil {
		t.Fatalf("count pages after denial: %v", err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE resource_type = ?`, logger.ResourcePage).Scan(&auditCount); err != nil {
		t.Fatalf("count page audits after denial: %v", err)
	}
	if pageCount != 0 || auditCount != 0 {
		t.Fatalf("denied create persisted pages/audits = %d/%d, want 0/0", pageCount, auditCount)
	}

	actor := AuditActor{UserID: data.UserID, Username: "testuser", Source: "mcp"}
	page, err := application.Create(actor, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Application page",
		Content:     "original",
	})
	if err != nil {
		t.Fatalf("administrator create: %v", err)
	}

	_, err = application.Update(actor, data.WorkspaceID, PageApplicationUpdateInput{ID: page.ID})
	if !errors.Is(err, ErrPageNoChanges) {
		t.Fatalf("empty update error = %v, want ErrPageNoChanges", err)
	}

	content := "updated"
	updated, err := application.Update(actor, data.WorkspaceID, PageApplicationUpdateInput{
		ID:      page.ID,
		Content: &content,
	})
	if err != nil {
		t.Fatalf("administrator update: %v", err)
	}
	if updated.Title != "Application page" || updated.Content != "updated" {
		t.Fatalf("partial update = title %q content %q", updated.Title, updated.Content)
	}

	rows, err := tdb.Query(`
		SELECT action_type, details
		FROM audit_logs
		WHERE resource_type = ? AND resource_id = ?
		ORDER BY id
	`, logger.ResourcePage, page.ID)
	if err != nil {
		t.Fatalf("query page audits: %v", err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, detailsJSON string
		if err := rows.Scan(&action, &detailsJSON); err != nil {
			t.Fatalf("scan page audit: %v", err)
		}
		var details map[string]interface{}
		if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
			t.Fatalf("decode page audit details: %v", err)
		}
		if details["source"] != "mcp" {
			t.Fatalf("audit %q source = %v, want mcp", action, details["source"])
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate page audits: %v", err)
	}
	want := []string{logger.ActionPageCreate, logger.ActionPageUpdate}
	if len(actions) != len(want) || actions[0] != want[0] || actions[1] != want[1] {
		t.Fatalf("page audit actions = %v, want %v", actions, want)
	}
}
