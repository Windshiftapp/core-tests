//go:build test

package jiraimport

import (
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestListMappingTargetsReturnsReusableEntities(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	service := New(tdb.Database, blobstore.Disabled())

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Target workspace', 'TGT') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var statusID int
	if err := tdb.QueryRow(`
		INSERT INTO statuses (name, category_id) VALUES ('Target status', 1) RETURNING id
	`).Scan(&statusID); err != nil {
		t.Fatalf("create status: %v", err)
	}
	var itemTypeID int
	if err := tdb.QueryRow(`
		INSERT INTO item_types (name) VALUES ('Target item type') RETURNING id
	`).Scan(&itemTypeID); err != nil {
		t.Fatalf("create item type: %v", err)
	}
	var customFieldID int
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type) VALUES ('Target field', 'text') RETURNING id
	`).Scan(&customFieldID); err != nil {
		t.Fatalf("create custom field: %v", err)
	}

	targets, err := service.ListMappingTargets()
	if err != nil {
		t.Fatalf("list mapping targets: %v", err)
	}

	assertTargetContains(t, "workspace", targets.Workspaces, workspaceID, func(target WorkspaceTarget) int {
		return target.ID
	})
	assertTargetContains(t, "status", targets.Statuses, statusID, func(target StatusTarget) int {
		return target.ID
	})
	assertTargetContains(t, "item type", targets.ItemTypes, itemTypeID, func(target ItemTypeTarget) int {
		return target.ID
	})
	assertTargetContains(t, "custom field", targets.CustomFields, customFieldID, func(target CustomFieldTarget) int {
		return target.ID
	})
}

func TestListMappingTargetsExcludesPersonalAndTemplateWorkspaces(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	service := New(tdb.Database, blobstore.Disabled())

	var personalID, templateID, inactiveID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, is_personal) VALUES ('Personal', 'PERS', true) RETURNING id
	`).Scan(&personalID); err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, is_template) VALUES ('Template', 'TMPL', true) RETURNING id
	`).Scan(&templateID); err != nil {
		t.Fatalf("create template workspace: %v", err)
	}
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active) VALUES ('Inactive', 'INAC', false) RETURNING id
	`).Scan(&inactiveID); err != nil {
		t.Fatalf("create inactive workspace: %v", err)
	}

	targets, err := service.ListMappingTargets()
	if err != nil {
		t.Fatalf("list mapping targets: %v", err)
	}
	for _, target := range targets.Workspaces {
		if target.ID == personalID || target.ID == templateID || target.ID == inactiveID {
			t.Fatalf("mapping targets include unusable workspace %#v", target)
		}
	}
}

func TestWorkspaceImportTargetRejectsUnusableWorkspaces(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	service := New(tdb.Database, blobstore.Disabled())

	var personalID, templateID, inactiveID, activeID int
	if err := tdb.QueryRow(`INSERT INTO workspaces (name, key, is_personal) VALUES ('Personal', 'PERS', true) RETURNING id`).Scan(&personalID); err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}
	if err := tdb.QueryRow(`INSERT INTO workspaces (name, key, is_template) VALUES ('Template', 'TMPL', true) RETURNING id`).Scan(&templateID); err != nil {
		t.Fatalf("create template workspace: %v", err)
	}
	if err := tdb.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Inactive', 'INAC', false) RETURNING id`).Scan(&inactiveID); err != nil {
		t.Fatalf("create inactive workspace: %v", err)
	}
	if err := tdb.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Active target', 'ACTV') RETURNING id`).Scan(&activeID); err != nil {
		t.Fatalf("create active workspace: %v", err)
	}

	for _, id := range []int{personalID, templateID, inactiveID} {
		target, err := service.WorkspaceImportTarget(id)
		if err != nil {
			t.Fatalf("workspace import target %d: %v", id, err)
		}
		if target != nil {
			t.Fatalf("workspace %d returned %#v, want nil", id, target)
		}
	}

	target, err := service.WorkspaceImportTarget(activeID)
	if err != nil {
		t.Fatalf("active workspace import target: %v", err)
	}
	if target == nil || target.ID != activeID || target.Key != "ACTV" || target.Name != "Active target" {
		t.Fatalf("active workspace target = %#v, want id=%d key=ACTV", target, activeID)
	}
}

func TestMappingTargetExistenceChecks(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	service := New(tdb.Database, blobstore.Disabled())

	entities := createCleanupGlobalEntities(t, tdb, "Target check")

	if exists, err := service.StatusExists(entities["status"]); err != nil || !exists {
		t.Fatalf("StatusExists(existing) = %t, %v; want true, nil", exists, err)
	}
	if exists, err := service.ItemTypeExists(entities["item_type"]); err != nil || !exists {
		t.Fatalf("ItemTypeExists(existing) = %t, %v; want true, nil", exists, err)
	}
	if exists, err := service.CustomFieldExists(entities["custom_field"]); err != nil || !exists {
		t.Fatalf("CustomFieldExists(existing) = %t, %v; want true, nil", exists, err)
	}
	if fieldType, err := service.CustomFieldTargetType(entities["custom_field"]); err != nil || fieldType == "" {
		t.Fatalf("CustomFieldTargetType(existing) = %q, %v; want non-empty, nil", fieldType, err)
	}
	if fieldType, err := service.CustomFieldTargetType(-1); err != nil || fieldType != "" {
		t.Fatalf("CustomFieldTargetType(missing) = %q, %v; want empty, nil", fieldType, err)
	}

	for name, check := range map[string]func(int) (bool, error){
		"status":       service.StatusExists,
		"item type":    service.ItemTypeExists,
		"custom field": service.CustomFieldExists,
	} {
		exists, err := check(-1)
		if err != nil {
			t.Fatalf("%s existence check for missing id: %v", name, err)
		}
		if exists {
			t.Fatalf("%s existence check for missing id = true, want false", name)
		}
	}
}

func TestCustomFieldTypesCompatible(t *testing.T) {
	tests := []struct {
		name       string
		targetType string
		sourceType string
		want       bool
	}{
		{name: "same type", targetType: "number", sourceType: "number", want: true},
		{name: "text into textarea", targetType: "textarea", sourceType: "text", want: true},
		{name: "textarea into text", targetType: "text", sourceType: "textarea", want: true},
		{name: "checkbox into boolean", targetType: "boolean", sourceType: "checkbox", want: true},
		{name: "text into select", targetType: "select", sourceType: "text", want: false},
		{name: "select into text", targetType: "text", sourceType: "select", want: false},
		{name: "multiselect into select", targetType: "select", sourceType: "multiselect", want: false},
		{name: "asset into text", targetType: "text", sourceType: "asset", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CustomFieldTypesCompatible(tt.targetType, tt.sourceType); got != tt.want {
				t.Fatalf("CustomFieldTypesCompatible(%q, %q) = %t, want %t", tt.targetType, tt.sourceType, got, tt.want)
			}
		})
	}
}

func assertTargetContains[T any](t *testing.T, label string, targets []T, id int, idOf func(T) int) {
	t.Helper()
	for _, target := range targets {
		if idOf(target) == id {
			return
		}
	}
	t.Fatalf("mapping targets contain no %s with id %d", label, id)
}
