//go:build test

package handlers

import (
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

// appendHierarchyContext is the formatting boundary for the Catch Me Up
// briefing's hierarchy context. It must stay additive: a leaf item with no
// parent or children contributes nothing.
func TestAppendHierarchyContextSkipsLeafItems(t *testing.T) {
	lines := appendHierarchyContext([]string{"Item: WI-1 - Leaf"}, nil, nil, nil, false)
	if len(lines) != 1 || lines[0] != "Item: WI-1 - Leaf" {
		t.Fatalf("expected the leaf item line unchanged, got %#v", lines)
	}
}

// Parent and direct sub-items must both reach the prompt with the fields the
// briefing renders.
func TestAppendHierarchyContextIncludesParentAndChildren(t *testing.T) {
	parent := &models.Item{
		WorkspaceKey:        "WI",
		WorkspaceItemNumber: 10,
		Title:               "Epic of everything",
		StatusName:          "In Progress",
		Description:         "Parent body",
	}
	parentComments := []services.ItemCommentSummary{
		{
			Author:    "Ada",
			CreatedAt: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
			Content:   "Parent decision",
		},
	}
	children := []repository.ChildItemSummary{
		{ItemKey: "WI-11", Title: "First child", ItemTypeName: "Task", StatusName: "Done", AssigneeName: "Grace", Description: "Child one body"},
		{ItemKey: "WI-12", Title: "Second child"},
	}

	lines := appendHierarchyContext(nil, parent, parentComments, children, false)
	got := strings.Join(lines, "\n")

	wantSubstrings := []string{
		"Parent item: WI-10 - Epic of everything",
		"Parent status: In Progress",
		"Parent body",
		"Parent recent comments:",
		"- Ada (Mar 4): Parent decision",
		"Sub-items (2):",
		"- WI-11: First child [Task] status=Done assignee=Grace",
		"  Description: Child one body",
		"- WI-12: Second child",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("expected context to contain %q, got:\n%s", want, got)
		}
	}
}

// Oversized parent descriptions and comments are truncated so a single item
// cannot dominate the briefing context.
func TestAppendHierarchyContextTruncatesLongFields(t *testing.T) {
	longDesc := strings.Repeat("d", catchMeUpMaxDescriptionBytes+500)
	longComment := strings.Repeat("c", catchMeUpMaxCommentBytes+200)
	parent := &models.Item{
		WorkspaceKey:        "WI",
		WorkspaceItemNumber: 10,
		Title:               "Big",
		Description:         longDesc,
	}
	parentComments := []services.ItemCommentSummary{
		{Author: "Ada", CreatedAt: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC), Content: longComment},
	}

	lines := appendHierarchyContext(nil, parent, parentComments, nil, false)
	got := strings.Join(lines, "\n")

	if strings.Contains(got, strings.Repeat("d", catchMeUpMaxDescriptionBytes+1)) {
		t.Errorf("expected the parent description to be truncated to %d bytes", catchMeUpMaxDescriptionBytes)
	}
	if !strings.Contains(got, strings.Repeat("d", catchMeUpMaxDescriptionBytes)+"...") {
		t.Error("expected the truncated parent description to end with an ellipsis")
	}
	if strings.Contains(got, strings.Repeat("c", catchMeUpMaxCommentBytes+1)) {
		t.Errorf("expected the parent comment to be truncated to %d bytes", catchMeUpMaxCommentBytes)
	}
	if !strings.Contains(got, strings.Repeat("c", catchMeUpMaxCommentBytes)+"...") {
		t.Error("expected the truncated parent comment to end with an ellipsis")
	}
}

// truncateBriefingText backs the description and comment caps. It must leave
// short text alone and never cut a multi-byte rune in half.
func TestTruncateBriefingText(t *testing.T) {
	t.Parallel()

	if got := truncateBriefingText("short", 100); got != "short" {
		t.Errorf("short text should be unchanged, got %q", got)
	}

	// "é" is two bytes. A 3-byte cut lands mid-rune and must back off to 2.
	if got := truncateBriefingText("aééé", 3); got != "aé..." {
		t.Errorf("expected a rune-safe cut at %q, got %q", "aé...", got)
	}
}

// Child descriptions get the same cap as other item descriptions.
func TestAppendHierarchyContextTruncatesChildDescription(t *testing.T) {
	children := []repository.ChildItemSummary{
		{ItemKey: "WI-11", Title: "First child", Description: strings.Repeat("e", catchMeUpMaxDescriptionBytes+500)},
	}

	got := strings.Join(appendHierarchyContext(nil, nil, nil, children, false), "\n")

	if strings.Contains(got, strings.Repeat("e", catchMeUpMaxDescriptionBytes+1)) {
		t.Errorf("expected the child description to be truncated to %d bytes", catchMeUpMaxDescriptionBytes)
	}
	if !strings.Contains(got, strings.Repeat("e", catchMeUpMaxDescriptionBytes)+"...") {
		t.Error("expected the truncated child description to end with an ellipsis")
	}
}

// A capped child list must say so, otherwise a large parent looks fully
// briefed when most of its sub-items were dropped.
func TestAppendHierarchyContextMarksTruncatedChildren(t *testing.T) {
	children := []repository.ChildItemSummary{
		{ItemKey: "WI-11", Title: "First child"},
	}

	lines := appendHierarchyContext(nil, nil, nil, children, true)
	got := strings.Join(lines, "\n")

	if !strings.Contains(got, "(showing the first 1 sub-items; more exist)") {
		t.Errorf("expected a truncation note, got:\n%s", got)
	}

	withoutNote := strings.Join(appendHierarchyContext(nil, nil, nil, children, false), "\n")
	if strings.Contains(withoutNote, "more exist") {
		t.Errorf("did not expect a truncation note, got:\n%s", withoutNote)
	}
}
