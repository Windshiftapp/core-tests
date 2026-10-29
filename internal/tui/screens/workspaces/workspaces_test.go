//go:build test

package workspaces

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"

	"windshift/internal/tui/core"
	"windshift/internal/tui/data"
	"windshift/internal/tui/styles"
)

func TestWorkspacePickerKeepsSelectionVisible(t *testing.T) {
	ctx := workspaceTestContext()
	m := New(ctx)
	m.requestID = 7
	m.SetSize(50, 5)
	workspaces := make([]data.Workspace, 12)
	for i := range workspaces {
		workspaces[i] = data.Workspace{ID: i + 1, Key: fmt.Sprintf("W%d", i+1), Name: fmt.Sprintf("Workspace %d", i+1)}
	}
	m.Update(data.WorkspacesLoadedMsg{Workspaces: workspaces, RequestID: 7})
	m.selected = 11
	m.ensureVisible()

	view := m.View()
	if !strings.Contains(view, "Workspace 12") {
		t.Fatalf("selected workspace is outside viewport: %q", view)
	}
	if strings.Contains(view, "Workspace 1\n") {
		t.Fatalf("viewport did not scroll away from the first row: %q", view)
	}
	if got := len(strings.Split(view, "\n")); got > 5 {
		t.Fatalf("view height = %d, want at most 5", got)
	}
}

func TestWorkspaceSpinnerStopsAfterLoading(t *testing.T) {
	m := New(workspaceTestContext())
	m.loading = false
	if cmd := m.Update(spinner.TickMsg{}); cmd != nil {
		t.Fatal("completed workspace load scheduled another spinner tick")
	}
}

func TestWorkspacePickerIgnoresStaleLoad(t *testing.T) {
	m := New(workspaceTestContext())
	m.requestID = 9
	m.Update(data.WorkspacesLoadedMsg{RequestID: 8, Workspaces: []data.Workspace{{ID: 1}}})
	if len(m.workspaces) != 0 || !m.loading {
		t.Fatalf("stale load mutated picker: workspaces=%v loading=%v", m.workspaces, m.loading)
	}
}

func workspaceTestContext() *core.Ctx {
	return &core.Ctx{
		Styles: styles.New(styles.CatppuccinMocha()),
		Theme:  styles.DefaultTheme,
		Keys:   core.DefaultKeyMap(),
	}
}
