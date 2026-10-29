//go:build test

package timelog

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"windshift/internal/tui/core"
	"windshift/internal/tui/data"
	"windshift/internal/tui/styles"
)

func TestNewUsesAuthenticatedUserTimezone(t *testing.T) {
	ctx := timeLogTestContext()
	ctx.User = &data.UserInfo{Timezone: "Asia/Tokyo"}
	ctx.Clock = func() time.Time { return time.Date(2026, 9, 2, 23, 30, 0, 0, time.UTC) }

	m := New(ctx, data.WorkItem{ID: 1}, nil)
	if got := m.dateInput.Value(); got != "2026-09-03" {
		t.Fatalf("date = %q, want user-local 2026-09-03", got)
	}
	if got := m.startTimeInput.Value(); got != "08:30" {
		t.Fatalf("start time = %q, want user-local 08:30", got)
	}
	if m.timezone != "Asia/Tokyo" {
		t.Fatalf("timezone = %q, want Asia/Tokyo", m.timezone)
	}
}

func TestNewSelectsOnlyAccessibleActiveDefaultProject(t *testing.T) {
	ctx := timeLogTestContext()
	workspaceDefault := 10
	ctx.Workspace.TimeProjectID = &workspaceDefault
	itemDefault := 20
	projects := []data.TimeProject{
		{ID: 10, Name: "Inactive", Status: "Archived"},
		{ID: 20, Name: "Active", Status: "Active"},
	}

	m := New(ctx, data.WorkItem{ID: 1, TimeProjectID: &itemDefault}, projects)
	if m.projectID == nil || *m.projectID != 20 || m.projectName != "Active" {
		t.Fatalf("selected project = %v %q, want active item default 20", m.projectID, m.projectName)
	}
	if len(m.timeProjects) != 1 || m.timeProjects[0].ID != 20 {
		t.Fatalf("bookable projects = %+v, want active project only", m.timeProjects)
	}
}

func TestValidateRejectsInvalidFieldsBeforeSubmission(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Model)
		want   string
	}{
		{"description", func(m *Model) { m.descInput.SetValue(" ") }, "description is required"},
		{"duration", func(m *Model) { m.durationInput.SetValue("5h30") }, "duration:"},
		{"date", func(m *Model) { m.dateInput.SetValue("tomorrow") }, "date must use"},
		{"time", func(m *Model) { m.startTimeInput.SetValue("25:00") }, "start time must use"},
		{"project", func(m *Model) { m.projectID = nil }, "project is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validTimeLogModel()
			tt.change(m)
			if err := m.validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestSaveSuppressesDuplicateSubmissionAndStaleCompletion(t *testing.T) {
	m := validTimeLogModel()
	save := tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"})
	first := m.handleKey(save)
	if first == nil || !m.submitting || m.requestID == 0 {
		t.Fatalf("first save = cmd %v submitting %v request %d", first != nil, m.submitting, m.requestID)
	}
	if second := m.handleKey(save); second != nil {
		t.Fatal("second save started another request")
	}
	requestID := m.requestID
	if cmd := m.Update(data.TimeLogCreatedMsg{ItemID: m.item.ID, RequestID: requestID + 1}); cmd != nil || !m.submitting {
		t.Fatal("stale completion dismissed or changed the active submission")
	}
}

func TestViewStaysWithinAssignedHeight(t *testing.T) {
	m := validTimeLogModel()
	m.SetSize(18, 5)
	if got := len(strings.Split(m.View(), "\n")); got > 5 {
		t.Fatalf("view height = %d, want at most 5", got)
	}
}

func TestViewUsesCanonicalItemKey(t *testing.T) {
	ctx := timeLogTestContext()
	m := New(ctx, data.WorkItem{ID: 900, Key: "WI-42", WorkspaceItemNumber: 42, Title: "Canonical"}, nil)
	view := m.View()
	if !strings.Contains(view, "WI-42") || strings.Contains(view, "WI-900") {
		t.Fatalf("time-log view does not use canonical key: %q", view)
	}
}

func validTimeLogModel() *Model {
	ctx := timeLogTestContext()
	projectID := 5
	m := New(ctx, data.WorkItem{ID: 41}, []data.TimeProject{{ID: projectID, Name: "Active", Status: "Active"}})
	m.descInput.SetValue("Implemented lifecycle fixes")
	m.durationInput.SetValue("1h30m")
	m.dateInput.SetValue("2026-09-02")
	m.startTimeInput.SetValue("09:15")
	m.projectID = &projectID
	m.projectName = "Active"
	return m
}

func timeLogTestContext() *core.Ctx {
	return &core.Ctx{
		Styles:    styles.New(styles.CatppuccinMocha()),
		Theme:     styles.DefaultTheme,
		Keys:      core.DefaultKeyMap(),
		Workspace: &data.Workspace{ID: 7, Key: "WI"},
		User:      &data.UserInfo{Timezone: "UTC"},
		Width:     80,
		Height:    24,
		Clock:     func() time.Time { return time.Date(2026, 9, 2, 9, 15, 0, 0, time.UTC) },
	}
}
