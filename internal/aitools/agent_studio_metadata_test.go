//go:build test

package aitools

import (
	"slices"
	"testing"
)

func TestDefaultRegistryEveryToolDeclaresAgentStudioMetadata(t *testing.T) {
	entries := Default.All()
	if len(entries) == 0 {
		t.Fatal("default registry is empty")
	}

	for _, entry := range entries {
		t.Run(entry.Name, func(t *testing.T) {
			if !validCapabilityGroup(entry.Group) {
				t.Fatalf("invalid capability group %q", entry.Group)
			}
			if !validAccessLevel(entry.Access) {
				t.Fatalf("invalid access level %q", entry.Access)
			}
			if !validRiskLevel(entry.Risk) {
				t.Fatalf("invalid risk level %q", entry.Risk)
			}
		})
	}
}

func TestStandardCapabilityGroupsExposeMandatoryPresetAndExcludeUnsafeTools(t *testing.T) {
	groups := StandardCapabilityGroups(Default)
	if len(groups) != 9 {
		t.Fatalf("expected 9 Standard capability groups, got %d", len(groups))
	}
	if groups[0].Key != CapabilityReadComment || !groups[0].Required {
		t.Fatalf("first group must be required Read and comment, got %#v", groups[0])
	}

	gotMandatory := make([]string, 0, len(groups[0].Tools))
	allTools := make([]string, 0)
	for _, group := range groups {
		for _, tool := range group.Tools {
			if tool.Access == AccessDestructive || tool.Access == AccessAdmin {
				t.Fatalf("unsafe tool %q exposed with access %q", tool.Name, tool.Access)
			}
			allTools = append(allTools, tool.Name)
			if group.Required {
				gotMandatory = append(gotMandatory, tool.Name)
			}
		}
	}

	wantMandatory := []string{
		"add_comment",
		"get_item",
		"get_item_children",
		"get_workspace",
		"list_comments",
		"list_items",
		"list_workspaces",
		"search_items",
	}
	if !slices.Equal(gotMandatory, wantMandatory) {
		t.Fatalf("mandatory preset mismatch\nwant: %v\n got: %v", wantMandatory, gotMandatory)
	}

	for _, excluded := range []string{
		"archive_page",
		"delete_comment",
		"delete_diagram",
		"delete_item",
		"grant_page_permission",
		"revoke_page_permission",
		"set_page_inheritance",
	} {
		if slices.Contains(allTools, excluded) {
			t.Fatalf("unsafe tool %q must not be available to Standard agents", excluded)
		}
	}
}
