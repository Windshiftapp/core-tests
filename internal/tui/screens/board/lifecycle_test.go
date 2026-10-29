//go:build test

package board

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"windshift/internal/tui/core"
	"windshift/internal/tui/data"
	"windshift/internal/tui/dialog"
	"windshift/internal/tui/styles"
)

func TestBoardUsesCanonicalItemKey(t *testing.T) {
	m := boardTestModel()
	item := data.WorkItem{ID: 900, Key: "WI-42", WorkspaceItemNumber: 42, WorkspaceKey: "WI", Title: "Canonical"}
	if got := m.itemKey(&item); got != "WI-42" {
		t.Fatalf("itemKey = %q, want WI-42", got)
	}
	if !(Filter{Query: "WI-42"}).Match(&item, "WRONG") {
		t.Fatal("canonical key did not match the board filter")
	}
	if (Filter{Query: "WI-900"}).Match(&item, "WI") {
		t.Fatal("database ID was exposed as a searchable item key")
	}
}

func TestBoardSpinnerStopsAfterItemLoad(t *testing.T) {
	m := boardTestModel()
	m.loading = false
	if cmd := m.Update(spinner.TickMsg{}); cmd != nil {
		t.Fatal("completed item load scheduled another spinner tick")
	}
}

func TestAuxiliaryFailureDoesNotStopItemLoading(t *testing.T) {
	m := boardTestModel()
	m.loadRequestID = 11
	m.loading = true
	m.Update(data.ErrorMsg{Operation: data.OpUsers, WorkspaceID: m.workspaceID, RequestID: 11, Err: "users failed"})
	if !m.loading {
		t.Fatal("assignable-user failure stopped work-item loading")
	}
}

func TestBoardIgnoresStaleWorkspaceResults(t *testing.T) {
	m := boardTestModel()
	m.loadRequestID = 12
	m.Update(data.WorkItemsLoadedMsg{
		WorkspaceID: 99, RequestID: 12,
		Items: []data.WorkItem{{ID: 1, Title: "wrong workspace"}},
	})
	if len(m.items) != 0 {
		t.Fatalf("stale workspace result mutated board: %+v", m.items)
	}
}

func TestDetailLazyLoadSuppressesDuplicateRequests(t *testing.T) {
	m := boardTestModel()
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One"}}
	m.rebuildRows()
	m.runState[1] = loadLoaded
	m.detailsLoaded[1] = true
	m.detailSeq = 4

	if cmd := m.Update(debounceMsg{seq: 4}); cmd == nil {
		t.Fatal("cold comments did not start a request")
	}
	firstRequest := m.commentRequests[1]
	if m.commentState[1] != loadLoading || firstRequest == 0 {
		t.Fatalf("comment state = %v request=%d, want loading with request", m.commentState[1], firstRequest)
	}
	m.Update(debounceMsg{seq: 4})
	if m.commentRequests[1] != firstRequest {
		t.Fatalf("duplicate debounce replaced in-flight request: %d -> %d", firstRequest, m.commentRequests[1])
	}
}

func TestCommentCompletionRefreshesOriginatingItem(t *testing.T) {
	m := boardTestModel()
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One"}, {ID: 2, Key: "WI-2", Title: "Two"}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[1]}})
	m.commentCreateRequests[3] = 1
	m.Update(data.CommentCreatedMsg{ItemID: 1, RequestID: 3})
	if m.commentState[1] != loadLoading || m.commentState[2] != loadStale {
		t.Fatalf("comment states = item1 %v item2 %v, want origin loading only", m.commentState[1], m.commentState[2])
	}
}

func TestBoardIgnoresCompletionFromPreviousScreenInstance(t *testing.T) {
	m := boardTestModel()
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One"}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})
	m.Update(data.CommentCreatedMsg{ItemID: 1, RequestID: 999})
	if m.commentState[1] != loadStale || m.commentRequests[1] != 0 {
		t.Fatalf("stale completion changed cache state: state %v request %d", m.commentState[1], m.commentRequests[1])
	}
}

func TestStatusPickerExcludesUnreachableAndNoOpStatuses(t *testing.T) {
	m := boardTestModel()
	statusID := 1
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One", StatusID: &statusID}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})
	m.detailsLoaded[1] = true
	m.statuses = []data.Status{{ID: 1, Name: "Open"}, {ID: 2, Name: "Unreachable"}}

	cmd := m.openStatusPicker()
	if cmd == nil {
		t.Fatal("no-transition status picker returned no feedback")
	}
	if _, ok := cmd().(core.NoticeMsg); !ok {
		t.Fatalf("no-transition command returned %T, want core.NoticeMsg", cmd())
	}
}

func TestValidTransitionsExcludesCurrentAndDuplicates(t *testing.T) {
	current := 1
	item := &data.WorkItem{StatusID: &current, Transitions: []data.Status{
		{ID: 1, Name: "Open"}, {ID: 2, Name: "Done"}, {ID: 2, Name: "Done twice"},
	}}
	got := validTransitions(item)
	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("validTransitions = %+v, want only Done", got)
	}
}

func TestQuickMutationKeepsAuthoritativeStateAndBlocksOverlap(t *testing.T) {
	m := boardTestModel()
	statusID := 1
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One", StatusID: &statusID, StatusName: "Open"}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})

	first := m.applyPickerResult(dialog.ResultMsg{ID: pickerStatusID, Value: data.Status{ID: 2, Name: "Done"}})
	if first == nil || *m.items[0].StatusID != 1 || m.items[0].StatusName != "Open" {
		t.Fatalf("first mutation changed local authoritative state: %+v", m.items[0])
	}
	second := m.applyPickerResult(dialog.ResultMsg{ID: pickerStatusID, Value: data.Status{ID: 3, Name: "Closed"}})
	if second == nil {
		t.Fatal("overlapping mutation returned no feedback")
	}
	if _, ok := second().(core.NoticeMsg); !ok {
		t.Fatalf("overlapping mutation returned %T, want core.NoticeMsg", second())
	}
}

func TestMutationFailureReloadsAuthoritativeItem(t *testing.T) {
	m := boardTestModel()
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One"}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})
	m.mutationRequests[1] = 15
	m.detailsLoaded[1] = true

	cmd := m.Update(data.ErrorMsg{Operation: data.OpItemStatus, ItemID: 1, RequestID: 15, Err: "transition rejected"})
	if cmd == nil {
		t.Fatal("mutation failure did not return reload and feedback commands")
	}
	if _, busy := m.mutationRequests[1]; busy {
		t.Fatal("failed mutation remained in flight")
	}
	if m.detailsLoaded[1] || m.detailRequests[1] == 0 {
		t.Fatalf("authoritative reload state = loaded %v request %d", m.detailsLoaded[1], m.detailRequests[1])
	}
}

func TestRunningAgentActivitySchedulesCorrelatedRefresh(t *testing.T) {
	m := boardTestModel()
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One"}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})
	m.runState[1] = loadLoading
	m.runRequests[1] = 20

	cmd := m.Update(data.AgentRunsLoadedMsg{
		ItemID: 1, RequestID: 20, Runs: []data.AgentRun{{ID: 4, Status: "running"}},
	})
	if cmd == nil || m.runState[1] != loadLoaded {
		t.Fatalf("running result = cmd %v state %v", cmd != nil, m.runState[1])
	}
	m.Update(agentRefreshMsg{itemID: 1, requestID: 20})
	if m.runState[1] != loadLoading || m.runRequests[1] == 20 {
		t.Fatalf("refresh state = %v request %d, want new loading request", m.runState[1], m.runRequests[1])
	}
}

func TestAgentFailureIsNotPresentedAsEmptySuccess(t *testing.T) {
	m := boardTestModel()
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One"}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})
	m.runState[1] = loadLoading
	m.runRequests[1] = 21
	m.Update(data.ErrorMsg{Operation: data.OpAgentRuns, ItemID: 1, RequestID: 21, Err: "agent API failed"})
	if m.runState[1] != loadFailed {
		t.Fatalf("agent state = %v, want failed", m.runState[1])
	}
}

func TestAssignableUsersResynchronizeAgentAssignee(t *testing.T) {
	m := boardTestModel()
	assigneeID := 8
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One", AssigneeID: &assigneeID}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})
	m.loadRequestID = 22
	m.Update(data.UsersLoadedMsg{
		WorkspaceID: m.workspaceID, RequestID: 22,
		Users: []data.User{{ID: assigneeID, FullName: "Agent", IsAgent: true}},
	})
	if !m.detail.agentAssignee {
		t.Fatal("assignable-user load did not resynchronize agent assignee state")
	}
}

func TestTimeLogWaitsForAuthenticatedTimezone(t *testing.T) {
	m := boardTestModel()
	m.items = []data.WorkItem{{ID: 1, Key: "WI-1", Title: "One"}}
	m.list.setRows([]Row{{Kind: RowItem, Item: &m.items[0]}})
	m.ctx.UserTimezoneKnown = false

	cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'l', Text: "l"}))
	if cmd == nil || m.pendingTimeLogItemID != 1 {
		t.Fatalf("timezone wait = cmd %v pending item %d", cmd != nil, m.pendingTimeLogItemID)
	}
	m.ctx.UserTimezoneKnown = true
	m.ctx.User = &data.UserInfo{Timezone: "Europe/Zurich"}
	push := m.Update(data.CurrentUserLoadedMsg{Timezone: "Europe/Zurich", OK: true})
	if push == nil {
		t.Fatal("timezone completion did not continue to the time-log screen")
	}
	if _, ok := push().(core.PushMsg); !ok {
		t.Fatalf("timezone completion returned %T, want core.PushMsg", push())
	}
}

func boardTestModel() *Model {
	workspace := &data.Workspace{ID: 7, Key: "WI", Name: "Windshift"}
	ctx := &core.Ctx{
		Styles:    styles.New(styles.CatppuccinMocha()),
		Theme:     styles.DefaultTheme,
		Keys:      core.DefaultKeyMap(),
		Workspace: workspace,
		Width:     100,
		Height:    30,
	}
	m := New(ctx)
	m.SetSize(100, 27)
	return m
}
