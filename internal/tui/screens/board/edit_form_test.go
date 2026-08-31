//go:build test

package board

import (
	"strings"
	"testing"

	"windshift/internal/tui/core"
	"windshift/internal/tui/data"
	"windshift/internal/tui/dialog"
	"windshift/internal/tui/styles"
)

func TestEditFormIncludesMutableWorkItemFields(t *testing.T) {
	statusID, priorityID, assigneeID := 1, 2, 3
	ctx := &core.Ctx{
		Styles:    styles.New(styles.WindshiftDark()),
		Theme:     styles.DefaultTheme,
		Keys:      core.DefaultKeyMap(),
		Workspace: &data.Workspace{ID: 1, Key: "WI", Name: "Windshift"},
		Width:     120,
		Height:    40,
	}
	m := New(ctx)
	m.statuses = []data.Status{{ID: statusID, Name: "Open"}}
	m.priorities = []data.Priority{{ID: priorityID, Name: "High"}}
	m.users = []data.User{{ID: assigneeID, Username: "ada", FullName: "Ada Lovelace"}}
	m.items = []data.WorkItem{{
		ID: 42, Title: "Restore editor", StatusID: &statusID, PriorityID: &priorityID, AssigneeID: &assigneeID,
	}}
	m.rebuildRows()

	msg := m.openEdit()()
	opened, ok := msg.(dialog.OpenMsg)
	if !ok {
		t.Fatalf("edit did not open a dialog: %#v", msg)
	}
	view := opened.Dialog.View(80, 32)
	for _, field := range []string{"Title", "Description", "Status", "Priority", "Assignee"} {
		if !strings.Contains(view, field) {
			t.Errorf("edit form is missing %s: %q", field, view)
		}
	}
	options, _ := m.assigneeFormOptions(&m.items[0])
	if len(options) < 2 || !strings.Contains(options[1].Search, "ada") {
		t.Fatalf("assignee picker does not index usernames: %#v", options)
	}
}
