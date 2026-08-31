package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/integrations/todoist"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// --- fakes ------------------------------------------------------------------

type fakeTodoistAPI struct {
	delta        *todoist.SyncResponse
	gotSyncToken string
	executed     []todoist.Command
}

func (f *fakeTodoistAPI) Sync(syncToken string, _ []string) (*todoist.SyncResponse, error) {
	f.gotSyncToken = syncToken
	if f.delta == nil {
		return &todoist.SyncResponse{SyncToken: "next"}, nil
	}
	return f.delta, nil
}

func (f *fakeTodoistAPI) ExecuteCommands(cmds []todoist.Command) (*todoist.SyncResponse, error) {
	f.executed = append(f.executed, cmds...)
	resp := &todoist.SyncResponse{
		TempIDMapping: map[string]string{},
		SyncStatus:    map[string]json.RawMessage{},
	}
	for i, c := range cmds {
		if c.TempID != "" {
			resp.TempIDMapping[c.TempID] = fmt.Sprintf("td-new-%d", i)
		}
		resp.SyncStatus[c.UUID] = json.RawMessage(`"ok"`)
	}
	return resp, nil
}

func (f *fakeTodoistAPI) commandsOfType(t string) []todoist.Command {
	var out []todoist.Command
	for _, c := range f.executed {
		if c.Type == t {
			out = append(out, c)
		}
	}
	return out
}

type fakeStore struct {
	tasks   map[int]taskState
	nextID  int
	created []taskState
	updated map[int]taskState
	deleted []int
}

func newFakeStore() *fakeStore {
	return &fakeStore{tasks: map[int]taskState{}, updated: map[int]taskState{}, nextID: 100}
}

func (s *fakeStore) ListTasks(int) ([]repository.PersonalWorkspaceTask, error) {
	ids := make([]int, 0, len(s.tasks))
	for id := range s.tasks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]repository.PersonalWorkspaceTask, 0, len(ids))
	for _, id := range ids {
		st := s.tasks[id]
		out = append(out, repository.PersonalWorkspaceTask{
			ItemID:      id,
			Title:       st.Title,
			Description: st.Description,
			DueDate:     dueTime(st.Due),
			Completed:   st.Completed,
		})
	}
	return out, nil
}

func (s *fakeStore) CreateTask(_, _ int, st taskState) (int, error) {
	s.nextID++
	s.tasks[s.nextID] = st
	s.created = append(s.created, st)
	return s.nextID, nil
}

func (s *fakeStore) UpdateTask(itemID int, st taskState, fields []string) error {
	cur := s.tasks[itemID]
	for _, f := range fields {
		switch f {
		case "title":
			cur.Title = st.Title
		case "description":
			cur.Description = st.Description
		case "due":
			cur.Due = st.Due
		case "completed":
			cur.Completed = st.Completed
		}
	}
	s.tasks[itemID] = cur
	s.updated[itemID] = cur
	return nil
}

func (s *fakeStore) DeleteTask(itemID int) error {
	delete(s.tasks, itemID)
	s.deleted = append(s.deleted, itemID)
	return nil
}

// --- harness ----------------------------------------------------------------

func newSyncHarness(t *testing.T) (*TodoistSyncService, database.Database) {
	t.Helper()
	db, err := database.NewSQLiteDB("file::memory:?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE todoist_task_links (
		id TEXT PRIMARY KEY, user_id TEXT NOT NULL, item_id INTEGER NOT NULL,
		todoist_task_id TEXT NOT NULL, todoist_project_id TEXT DEFAULT '',
		last_title TEXT DEFAULT '', last_description TEXT DEFAULT '', last_due TEXT DEFAULT '',
		last_priority INTEGER DEFAULT 1, last_completed BOOLEAN DEFAULT FALSE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, todoist_task_id), UNIQUE(item_id)
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	svc := &TodoistSyncService{db: db, syncRepo: repository.NewTodoistSyncRepository(db)}
	return svc, db
}

func baseConfig() models.TodoistSyncConfig {
	return models.TodoistSyncConfig{
		ID: "cfg", UserID: "5", IntegrationProviderID: "prov", PersonalWorkspaceID: 1,
		Enabled: true, ScopeMode: models.TodoistScopeAll, SyncToken: "tok-0",
	}
}

// seedLink inserts an existing mapping with a snapshot.
func seedLink(t *testing.T, svc *TodoistSyncService, itemID int, todoistID string, snap taskState) {
	t.Helper()
	err := svc.syncRepo.UpsertLink(models.TodoistTaskLink{
		ID: "link-" + todoistID, UserID: "5", ItemID: itemID, TodoistTaskID: todoistID,
		LastTitle: snap.Title, LastDescription: snap.Description, LastDue: snap.Due, LastCompleted: snap.Completed,
	})
	if err != nil {
		t.Fatalf("seed link: %v", err)
	}
}

// --- tests ------------------------------------------------------------------

func TestReconcileResolveMatrix(t *testing.T) {
	// ws, td, snap -> winner
	cases := []struct{ ws, td, snap, want string }{
		{"A", "A", "A", "A"}, // unchanged
		{"B", "A", "A", "B"}, // WS-only change
		{"A", "C", "A", "C"}, // TD-only change
		{"B", "C", "A", "C"}, // both changed, disagree -> TD wins
		{"B", "B", "A", "B"}, // both changed, agree
	}
	for _, c := range cases {
		if got := resolve(c.ws, c.td, c.snap); got != c.want {
			t.Errorf("resolve(%q,%q,%q) = %q, want %q", c.ws, c.td, c.snap, got, c.want)
		}
	}
}

func TestReconcileNewTodoistTaskCreatesWSItem(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	api := &fakeTodoistAPI{delta: &todoist.SyncResponse{
		SyncToken: "next",
		Items:     []todoist.Item{{ID: "td-1", ProjectID: "p1", Content: "Buy milk", Due: &todoist.Due{Date: "2026-07-01"}}},
	}}

	stats, token, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if token != "next" || api.gotSyncToken != "tok-0" {
		t.Errorf("token plumbing wrong: sent %q got back %q", api.gotSyncToken, token)
	}
	if stats.CreatedInWS != 1 || len(store.created) != 1 {
		t.Fatalf("expected 1 WS create, got stats=%+v created=%+v", stats, store.created)
	}
	if store.created[0].Title != "Buy milk" || store.created[0].Due != "2026-07-01" {
		t.Errorf("created task wrong: %+v", store.created[0])
	}
	// A mapping should now exist for td-1.
	if _, err := svc.syncRepo.GetLinkByTodoistID("5", "td-1"); err != nil {
		t.Errorf("expected link for td-1: %v", err)
	}
}

func TestReconcileNewWSTaskCreatesTodoistTask(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	store.tasks[100] = taskState{Title: "Write report", Due: "2026-08-01"}
	api := &fakeTodoistAPI{}

	stats, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	adds := api.commandsOfType("item_add")
	if len(adds) != 1 || stats.CreatedInTD != 1 {
		t.Fatalf("expected 1 item_add, got %d (stats %+v)", len(adds), stats)
	}
	// Link saved with the real id from temp_id_mapping.
	link, err := svc.syncRepo.GetLinkByItemID("5", 100)
	if err != nil {
		t.Fatalf("expected link for item 100: %v", err)
	}
	if link.TodoistTaskID == "" || link.LastTitle != "Write report" {
		t.Errorf("link not resolved from temp id: %+v", link)
	}
}

func TestReconcileTodoistCompletionMirrorsToWS(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	store.tasks[100] = taskState{Title: "Task", Completed: false}
	seedLink(t, svc, 100, "td-1", taskState{Title: "Task", Completed: false})
	api := &fakeTodoistAPI{delta: &todoist.SyncResponse{
		SyncToken: "next",
		Items:     []todoist.Item{{ID: "td-1", ProjectID: "p1", Content: "Task", Checked: true}},
	}}

	stats, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stats.UpdatedInWS != 1 || !store.tasks[100].Completed {
		t.Fatalf("expected WS task completed, stats=%+v task=%+v", stats, store.tasks[100])
	}
}

func TestReconcileWSCompletionMirrorsToTodoist(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	store.tasks[100] = taskState{Title: "Task", Completed: true}
	seedLink(t, svc, 100, "td-1", taskState{Title: "Task", Completed: false})
	api := &fakeTodoistAPI{} // empty delta: Todoist unchanged

	_, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := api.commandsOfType("item_complete"); len(got) != 1 {
		t.Fatalf("expected 1 item_complete, got %d", len(got))
	}
}

func TestReconcileTodoistDeletionRemovesWSItem(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	store.tasks[100] = taskState{Title: "Task"}
	seedLink(t, svc, 100, "td-1", taskState{Title: "Task"})
	api := &fakeTodoistAPI{delta: &todoist.SyncResponse{
		SyncToken: "next",
		Items:     []todoist.Item{{ID: "td-1", ProjectID: "p1", IsDeleted: true}},
	}}

	stats, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stats.DeletedInWS != 1 || len(store.deleted) != 1 {
		t.Fatalf("expected WS delete, stats=%+v deleted=%+v", stats, store.deleted)
	}
	if _, err := svc.syncRepo.GetLinkByTodoistID("5", "td-1"); err == nil {
		t.Error("link should be removed after TD deletion")
	}
}

func TestReconcileWSDeletionRemovesTodoistTask(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore() // item 100 absent => deleted in WS
	seedLink(t, svc, 100, "td-1", taskState{Title: "Task"})
	api := &fakeTodoistAPI{}

	stats, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := api.commandsOfType("item_delete"); len(got) != 1 || stats.DeletedInTD != 1 {
		t.Fatalf("expected 1 item_delete, got %d (stats %+v)", len(got), stats)
	}
	if _, err := svc.syncRepo.GetLinkByItemID("5", 100); err == nil {
		t.Error("link should be removed after WS deletion")
	}
}

func TestReconcileConflictTodoistWins(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	store.tasks[100] = taskState{Title: "WS edit"} // WS changed title
	seedLink(t, svc, 100, "td-1", taskState{Title: "original"})
	api := &fakeTodoistAPI{delta: &todoist.SyncResponse{
		SyncToken: "next",
		Items:     []todoist.Item{{ID: "td-1", ProjectID: "p1", Content: "TD edit"}}, // TD also changed
	}}

	_, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.tasks[100].Title != "TD edit" {
		t.Errorf("conflict should resolve to Todoist value, got %q", store.tasks[100].Title)
	}
	if got := api.commandsOfType("item_update"); len(got) != 0 {
		t.Errorf("no TD update expected (TD already holds winner), got %d", len(got))
	}
}

func TestReconcileWSOnlyEditPushesToTodoist(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	store.tasks[100] = taskState{Title: "new WS title"}
	seedLink(t, svc, 100, "td-1", taskState{Title: "old title"})
	api := &fakeTodoistAPI{} // TD unchanged

	_, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updates := api.commandsOfType("item_update")
	if len(updates) != 1 {
		t.Fatalf("expected 1 item_update, got %d", len(updates))
	}
	// snapshot advanced to the pushed value
	link, _ := svc.syncRepo.GetLinkByItemID("5", 100)
	if link.LastTitle != "new WS title" {
		t.Errorf("snapshot not advanced: %+v", link)
	}
}

func TestReconcileOutOfScopeTodoistTaskIgnored(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	cfg := baseConfig()
	cfg.ScopeMode = models.TodoistScopeProject
	cfg.TodoistProjectID = "p-target"
	api := &fakeTodoistAPI{delta: &todoist.SyncResponse{
		SyncToken: "next",
		Items:     []todoist.Item{{ID: "td-9", ProjectID: "p-other", Content: "Not mine"}},
	}}

	stats, _, err := svc.reconcile(cfg, api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stats.CreatedInWS != 0 || len(store.created) != 0 {
		t.Fatalf("out-of-scope task should be ignored, stats=%+v", stats)
	}
}

// TestReconcileSanitizesInboundTodoistContent proves Todoist task content —
// external, attacker-influenceable input — is sanitized at the ingress boundary
// before it lands in a Windshift item OR in the last-synced snapshot. Without
// this, raw HTML/script titles and javascript: Markdown links would be written
// through the internal item paths that assume pre-sanitized input.
func TestReconcileSanitizesInboundTodoistContent(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	api := &fakeTodoistAPI{delta: &todoist.SyncResponse{
		SyncToken: "next",
		Items: []todoist.Item{{
			ID: "td-1", ProjectID: "p1",
			Content:     "<script>alert('xss')</script>Buy milk",
			Description: "See [click](javascript:alert(1)) for details",
		}},
	}}

	_, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 WS create, got %d", len(store.created))
	}
	got := store.created[0]
	if strings.Contains(got.Title, "<script") || strings.Contains(got.Title, "alert") {
		t.Errorf("inbound title not sanitized: %q", got.Title)
	}
	if got.Title != "Buy milk" {
		t.Errorf("title = %q, want %q", got.Title, "Buy milk")
	}
	if strings.Contains(strings.ToLower(got.Description), "javascript:") {
		t.Errorf("inbound description retained dangerous URL: %q", got.Description)
	}

	// The snapshot must store the SANITIZED form, otherwise every subsequent
	// sync would see ws(sanitized) != snapshot(raw) and churn forever.
	link, err := svc.syncRepo.GetLinkByTodoistID("5", "td-1")
	if err != nil {
		t.Fatalf("expected link for td-1: %v", err)
	}
	if link.LastTitle != got.Title || link.LastDescription != got.Description {
		t.Errorf("snapshot not sanitized: snap=(%q,%q) task=(%q,%q)",
			link.LastTitle, link.LastDescription, got.Title, got.Description)
	}
}

// TestSyncConfigRejectsConcurrentRun proves the per-config admission lock: while
// one run holds the lock, a second SyncConfig returns ErrTodoistSyncAlreadyRunning
// and never reaches the Todoist API (no double-create). The service harness'
// newAPI/newStore are wired to fail the test if invoked while the lock is held.
func TestSyncConfigRejectsConcurrentRun(t *testing.T) {
	svc, db := newSyncHarness(t)
	createSyncConfigTableForLock(t, db)

	cfg := baseConfig()
	if err := svc.syncRepo.UpsertConfig(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// Simulate an in-flight run holding the lock.
	now := time.Now().UTC()
	ok, err := svc.syncRepo.AcquireSyncLock(cfg.ID, now, now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("precondition acquire: ok=%v err=%v", ok, err)
	}

	svc.newAPI = func(string) todoistAPI {
		t.Fatal("Todoist API must not be built while another run holds the lock")
		return nil
	}
	svc.newStore = func() personalStore {
		t.Fatal("personal store must not be built while another run holds the lock")
		return nil
	}

	if _, err := svc.SyncConfig(cfg); !errors.Is(err, ErrTodoistSyncAlreadyRunning) {
		t.Fatalf("SyncConfig with lock held = %v, want ErrTodoistSyncAlreadyRunning", err)
	}
}

// createSyncConfigTableForLock adds the todoist_sync_config table (incl. the
// sync_lock_until lock column) to the in-memory service harness DB, which by
// default only carries todoist_task_links.
func createSyncConfigTableForLock(t *testing.T, db database.Database) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE todoist_sync_config (
		id TEXT PRIMARY KEY, user_id TEXT NOT NULL, integration_provider_id TEXT NOT NULL,
		personal_workspace_id INTEGER NOT NULL, enabled BOOLEAN DEFAULT FALSE,
		scope_mode TEXT NOT NULL DEFAULT 'all', todoist_project_id TEXT DEFAULT '',
		sync_token TEXT DEFAULT '*', last_synced_at DATETIME, last_error TEXT DEFAULT '',
		sync_lock_until DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, integration_provider_id)
	)`); err != nil {
		t.Fatalf("create todoist_sync_config: %v", err)
	}
}

func TestReconcileInSyncPairNoOp(t *testing.T) {
	svc, _ := newSyncHarness(t)
	store := newFakeStore()
	store.tasks[100] = taskState{Title: "Task", Completed: false}
	seedLink(t, svc, 100, "td-1", taskState{Title: "Task", Completed: false})
	api := &fakeTodoistAPI{}

	stats, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if (stats != SyncStats{}) {
		t.Errorf("expected no changes, got %+v", stats)
	}
	if len(api.executed) != 0 {
		t.Errorf("expected no commands, got %d", len(api.executed))
	}
}
