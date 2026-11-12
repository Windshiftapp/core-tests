//go:build test

package handlers

import (
	"testing"

	"windshift/internal/jira"
	"windshift/internal/jiraimport"
	"windshift/internal/models"
)

func TestMapJiraWorkflowGuards(t *testing.T) {
	t.Parallel()

	principals := jiraGuardPrincipals{
		groupsByName: map[string]int{"developers": 7},
		rolesByName:  map[string]int{"developers": 9},
	}
	guards := []jira.JiraWorkflowGuard{
		{Kind: "user_in_group", Mode: "condition", Args: map[string]any{"groupName": "Developers"}},
		{Kind: "user_in_role", Mode: "condition", Args: map[string]any{"projectRole": "Developers"}},
		{Kind: "permission", Mode: "condition", Args: map[string]any{"permission": "Resolve Issue"}},
		{Kind: "user_in_group", Mode: "condition", Args: map[string]any{"groupName": "Missing"}},
		{Kind: "only_assignee", Mode: "condition"},
	}

	mapped := mapJiraWorkflowGuards(guards, principals)
	if len(mapped) != len(guards) {
		t.Fatalf("mapped %d guards, want %d", len(mapped), len(guards))
	}

	if mapped[0].ConditionType != models.ConditionTypeUserInGroup || !mapped[0].Mappable() {
		t.Fatalf("group guard = %#v, want a mappable user_in_group", mapped[0])
	}
	if got := mapped[0].Config["group_id"]; got != 7 {
		t.Fatalf("group_id = %v, want 7", got)
	}
	if mapped[0].Config["source"] != "current_user" {
		t.Fatalf("group source = %v, want current_user", mapped[0].Config["source"])
	}
	if mapped[1].ConditionType != models.ConditionTypeUserInRole || mapped[1].Config["role_id"] != 9 {
		t.Fatalf("role guard = %#v, want a mappable user_in_role with role_id 9", mapped[1])
	}
	for _, index := range []int{2, 3, 4} {
		if mapped[index].Mappable() {
			t.Fatalf("guard %d = %#v, want unmappable", index, mapped[index])
		}
	}
}

func TestJiraGuardMetadataCountsMappedAndLocked(t *testing.T) {
	t.Parallel()

	edges := []jiraimport.WorkflowEdge{
		{ToStatusID: 1, Guards: []jiraimport.WorkflowGuard{
			{Kind: "user_in_group", ConditionType: models.ConditionTypeUserInGroup},
			{Kind: "permission"},
		}},
		{ToStatusID: 2, Guards: []jiraimport.WorkflowGuard{{Kind: "permission"}}},
	}
	metadata := jiraGuardMetadata(edges)
	if metadata["mapped_guard_count"] != 1 {
		t.Fatalf("mapped_guard_count = %v, want 1", metadata["mapped_guard_count"])
	}
	if metadata["locked_guard_count"] != 2 {
		t.Fatalf("locked_guard_count = %v, want 2", metadata["locked_guard_count"])
	}
	kinds, ok := metadata["guard_kinds"].(map[string]int)
	if !ok || kinds["permission"] != 2 || kinds["user_in_group"] != 1 {
		t.Fatalf("guard_kinds = %#v, want permission=2 user_in_group=1", metadata["guard_kinds"])
	}
}

func TestAuthoritativeJiraWorkflowEdgesAttachMappedGuards(t *testing.T) {
	t.Parallel()

	group := &jiraImportWorkflowGroup{
		statusIDs: []int{11, 22},
		transitions: []jira.JiraConfiguredWorkflowTransition{{
			ID:             "done",
			Type:           jira.JiraWorkflowTransitionDirected,
			FromStatusIDs:  []string{"a"},
			ToStatusID:     "b",
			ConditionCount: 1,
			Guards: []jira.JiraWorkflowGuard{{
				Kind: "user_in_group", Mode: "condition", Args: map[string]any{"groupName": "Developers"},
			}},
		}},
	}
	statusMap := map[string]int{"a": 11, "b": 22}
	principals := jiraGuardPrincipals{groupsByName: map[string]int{"developers": 7}}

	edges := authoritativeJiraWorkflowEdges(group, statusMap, principals)
	var found *jiraImportWorkflowEdge
	for index := range edges {
		if edges[index].fromStatusID != nil && *edges[index].fromStatusID == 11 && edges[index].toStatusID == 22 {
			found = &edges[index]
		}
	}
	if found == nil {
		t.Fatalf("edge 11->22 missing from %#v", edges)
	}
	if len(found.guards) != 1 {
		t.Fatalf("edge guards = %#v, want one mapped guard", found.guards)
	}
	if found.guards[0].ConditionType != models.ConditionTypeUserInGroup || found.guards[0].Config["group_id"] != 7 {
		t.Fatalf("edge guard = %#v, want user_in_group group_id 7", found.guards[0])
	}
}
