//go:build test

package handlers

import (
	"strings"
	"testing"
)

func TestBuildChatContextHintForCurrentPage(t *testing.T) {
	hint := buildChatContextHint(&ChatContext{
		View:        "workspace-pages",
		WorkspaceID: 42,
		PageID:      9,
	})

	for _, want := range []string{
		"currently viewing knowledge page 9 in workspace 42",
		"call get_page with page_id=9",
		"call update_page with page_id=9",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint missing %q:\n%s", want, hint)
		}
	}
}

func TestBuildChatContextHintForWorkspacePagesIndex(t *testing.T) {
	hint := buildChatContextHint(&ChatContext{
		View:        "workspace-pages",
		WorkspaceID: 42,
	})

	for _, want := range []string{
		"knowledge pages area for workspace 42",
		"use list_pages or search_knowledge in workspace_id=42",
		"use create_page in this workspace",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint missing %q:\n%s", want, hint)
		}
	}
}

func TestBuildChatContextHintForCurrentItemByID(t *testing.T) {
	hint := buildChatContextHint(&ChatContext{
		View:        "item-detail",
		WorkspaceID: 42,
		ItemID:      123,
	})

	for _, want := range []string{
		"currently viewing work item 123 in workspace 42",
		"call get_item with item_id=123",
		"Use update_item with item_id=123",
		"transition_item with item_id=123",
		"add_comment with item_id=123",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint missing %q:\n%s", want, hint)
		}
	}
}

func TestBuildChatContextHintForCurrentPersonalItem(t *testing.T) {
	hint := buildChatContextHint(&ChatContext{
		View:   "item-detail",
		ItemID: 123,
	})

	if strings.Contains(hint, "workspace 0") {
		t.Fatalf("hint should not mention workspace 0:\n%s", hint)
	}
	if !strings.Contains(hint, "currently viewing work item 123") {
		t.Fatalf("hint missing current item:\n%s", hint)
	}
}

func TestBuildChatContextHintForCurrentItemByKey(t *testing.T) {
	hint := buildChatContextHint(&ChatContext{
		View:    "item-detail",
		ItemKey: "WI-348",
	})

	for _, want := range []string{
		"currently viewing work item WI-348",
		"call get_item with item_key=\"WI-348\"",
		"Use update_item with item_key=\"WI-348\"",
		"transition_item with item_key=\"WI-348\"",
		"add_comment with item_key=\"WI-348\"",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint missing %q:\n%s", want, hint)
		}
	}
}

func TestBuildChatContextHintIgnoresUnsupportedViews(t *testing.T) {
	if hint := buildChatContextHint(&ChatContext{View: "homepage"}); hint != "" {
		t.Fatalf("expected empty hint, got %q", hint)
	}
}
