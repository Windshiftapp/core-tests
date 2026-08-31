package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"testing"
	"time"
)

// World is a deterministic dataset seeded by SeedWorld. Tests assert filter
// resultsets against the typed handles below — any "list items in milestone
// Q1" assertion compares against world.ItemsInMilestone("Q1") so the test
// stays correct as IDs shift between runs.
//
// The shape is hand-maintained: each item is positioned at a chosen
// intersection of workspace × status × milestone × iteration × labels ×
// assignee so that every filter dimension has both hits and non-hits.
type World struct {
	Alpha       WorkspaceFx
	Beta        WorkspaceFx
	Statuses    StatusSetFx
	Milestones  MilestonesFx
	Iterations  IterationsFx
	Labels      LabelsFx
	Users       UsersFx
	Items       []ItemFx
	defaultType int // regular item type ID used for create requests
}

type WorkspaceFx struct {
	ID  int
	Key string
}

// StatusSetFx holds workspace-scoped status IDs. SeedWorld populates these
// from the workspace's pre-seeded default statuses (Open / In Progress /
// Done) — the default workflow has transitions wired between them, so
// `task move` actually works against them. Adding extra statuses would
// require also seeding workflow_transitions rows, which is out of scope.
type StatusSetFx struct {
	Open       int
	InProgress int
	Done       int
}

type MilestoneFx struct {
	ID   int
	Name string
}

type MilestonesFx struct {
	Q1, Q2, Backlog, Released MilestoneFx
}

type IterationFx struct {
	ID     int
	Name   string
	Status string // "planned" / "active" / "completed"
}

type IterationsFx struct {
	Sprint1, Sprint2, Sprint3 IterationFx
}

type LabelFx struct {
	ID   int
	Name string
}

type LabelsFx struct {
	Bug, Feature, Tech, Customer LabelFx
}

type UserFx struct {
	ID       int
	Username string
}

type UsersFx struct {
	Admin, Alice, Bob UserFx
}

// ItemFx records the deterministic placement of a seeded item. Tests should
// not hardcode ID — read from world.Items[i].ID.
type ItemFx struct {
	ID          int
	Key         string // "ALPHA-1" etc., as the server assigns it
	WorkspaceID int
	Title       string
	StatusID    int
	StatusName  string
	MilestoneID int // 0 if none
	IterationID int // 0 if none
	AssigneeID  int // 0 if unassigned
	LabelIDs    []int
}

// SeedWorld creates the fixture world against testServer. It mints two
// workspaces, statuses (5 per workspace), milestones, iterations, labels,
// users, and ~14 items spread across the dimensions. Authentication uses the
// admin bearer token CreateBearerToken put on the testServer.
func SeedWorld(t *testing.T, ts *TestServer) *World {
	t.Helper()
	w := &World{}

	// 1. Workspaces ----------------------------------------------------------
	w.Alpha.ID, w.Alpha.Key = CreateTestWorkspace(t, ts, "Alpha", shortKey("ALPHA"))
	w.Beta.ID, w.Beta.Key = CreateTestWorkspace(t, ts, "Beta", shortKey("BETA"))

	// 2. Statuses — use the workspace's default Open / In Progress / Done.
	// They already have workflow transitions wired (Open→{Open,InProgress,Done}).
	w.Statuses = lookupDefaultStatuses(t, ts, w.Alpha.ID)

	// Use a regular type explicitly. Map iteration is nondeterministic and can
	// otherwise select the generic Sub-task type, which requires a parent.
	configSetID := GetDefaultConfigurationSet(t, ts)
	itemTypes := GetItemTypes(t, ts, configSetID)
	w.defaultType = RequireItemTypeID(t, itemTypes, "Task")

	// 3. Milestones (in Alpha) ----------------------------------------------
	w.Milestones.Q1 = createMilestoneFx(t, ts, w.Alpha.ID, "Q1 Plan", "in-progress")
	w.Milestones.Q2 = createMilestoneFx(t, ts, w.Alpha.ID, "Q2 Plan", "planning")
	w.Milestones.Backlog = createMilestoneFx(t, ts, w.Alpha.ID, "Backlog", "planning")
	w.Milestones.Released = createMilestoneFx(t, ts, w.Alpha.ID, "Released", "completed")

	// 4. Iterations (in Alpha) ----------------------------------------------
	w.Iterations.Sprint1 = createIterationFx(t, ts, w.Alpha.ID, "Sprint 1", "planned")
	w.Iterations.Sprint2 = createIterationFx(t, ts, w.Alpha.ID, "Sprint 2", "active")
	w.Iterations.Sprint3 = createIterationFx(t, ts, w.Alpha.ID, "Sprint 3", "completed")

	// 5. Labels (in Alpha) --------------------------------------------------
	w.Labels.Bug = createLabelFx(t, ts, w.Alpha.ID, "bug", "#ef4444")
	w.Labels.Feature = createLabelFx(t, ts, w.Alpha.ID, "feature", "#22c55e")
	w.Labels.Tech = createLabelFx(t, ts, w.Alpha.ID, "tech-debt", "#a855f7")
	w.Labels.Customer = createLabelFx(t, ts, w.Alpha.ID, "customer", "#f59e0b")

	// 6. Users --------------------------------------------------------------
	w.Users.Admin = lookupAdminUser(t, ts)
	aliceID, aliceUser, _ := CreateTestUserWithCredentials(t, ts, "world_alice", "alice@world.test")
	w.Users.Alice = UserFx{ID: aliceID, Username: aliceUser}
	bobID, bobUser, _ := CreateTestUserWithCredentials(t, ts, "world_bob", "bob@world.test")
	w.Users.Bob = UserFx{ID: bobID, Username: bobUser}

	// 7. Items ---------------------------------------------------------------
	// The matrix below is the source of truth. Every later filter assertion
	// must match the placement here. Comments record the (status, milestone,
	// iteration, labels, assignee) so reviewers can audit at a glance.
	type seed struct {
		Title       string
		WorkspaceID int
		StatusID    int
		StatusName  string
		MilestoneID int
		IterationID int
		AssigneeID  int
		LabelIDs    []int
	}
	seeds := []seed{
		// 0  Alpha, Open,        Q1,        Sprint1, [Bug],            Alice
		{"alpha bug rfc", w.Alpha.ID, w.Statuses.Open, "Open", w.Milestones.Q1.ID, w.Iterations.Sprint1.ID, aliceID, []int{w.Labels.Bug.ID}},
		// 1  Alpha, InProgress,  Q1,        Sprint1, [Feature],        Alice
		{"alpha feature ramp", w.Alpha.ID, w.Statuses.InProgress, "InProgress", w.Milestones.Q1.ID, w.Iterations.Sprint1.ID, aliceID, []int{w.Labels.Feature.ID}},
		// 2  Alpha, Done,        Q1,        Sprint2, [Bug, Tech],      Bob
		{"alpha old bug", w.Alpha.ID, w.Statuses.Done, "Done", w.Milestones.Q1.ID, w.Iterations.Sprint2.ID, bobID, []int{w.Labels.Bug.ID, w.Labels.Tech.ID}},
		// 3  Alpha, InProgress,  Q2,        Sprint2, [Feature],        Bob
		{"alpha next quarter feature", w.Alpha.ID, w.Statuses.InProgress, "InProgress", w.Milestones.Q2.ID, w.Iterations.Sprint2.ID, bobID, []int{w.Labels.Feature.ID}},
		// 4  Alpha, Open,        Backlog,   —,       [Tech],           —
		{"alpha tech backlog", w.Alpha.ID, w.Statuses.Open, "Open", w.Milestones.Backlog.ID, 0, 0, []int{w.Labels.Tech.ID}},
		// 5  Alpha, Done,        Released,  —,       [Customer],       Alice
		{"alpha customer ship", w.Alpha.ID, w.Statuses.Done, "Done", w.Milestones.Released.ID, 0, aliceID, []int{w.Labels.Customer.ID}},
		// 6  Alpha, InProgress,  Q2,        Sprint3, [Customer, Bug],  Bob
		{"alpha customer regression", w.Alpha.ID, w.Statuses.InProgress, "InProgress", w.Milestones.Q2.ID, w.Iterations.Sprint3.ID, bobID, []int{w.Labels.Customer.ID, w.Labels.Bug.ID}},
		// 7  Alpha, Done,        Q1,        —,       [Feature],        —
		{"alpha cancelled work", w.Alpha.ID, w.Statuses.Done, "Done", w.Milestones.Q1.ID, 0, 0, []int{w.Labels.Feature.ID}},
		// 8  Alpha, Open,        —,         Sprint1, [],               Alice
		{"alpha unassigned milestone", w.Alpha.ID, w.Statuses.Open, "Open", 0, w.Iterations.Sprint1.ID, aliceID, nil},
		// 9  Alpha, Open,        —,         —,       [],               —
		{"alpha unstructured", w.Alpha.ID, w.Statuses.Open, "Open", 0, 0, 0, nil},
		// 10 Alpha, InProgress,  —,         Sprint2, [Tech, Feature],  Bob
		{"alpha sprint2 only", w.Alpha.ID, w.Statuses.InProgress, "InProgress", 0, w.Iterations.Sprint2.ID, bobID, []int{w.Labels.Tech.ID, w.Labels.Feature.ID}},
		// 11 Beta,  Open,        —,         —,       [],               —
		{"beta only item one", w.Beta.ID, 0, "", 0, 0, 0, nil},
		// 12 Beta,  Open,        —,         —,       [],               Alice
		{"beta only item two", w.Beta.ID, 0, "", 0, 0, aliceID, nil},
	}

	for _, s := range seeds {
		fx := createItemFx(t, ts, s.WorkspaceID, s.Title, s.StatusID, s.MilestoneID, s.IterationID, s.AssigneeID, w.defaultType)
		fx.StatusName = s.StatusName
		if len(s.LabelIDs) > 0 {
			setItemLabels(t, ts, fx.ID, s.LabelIDs)
			fx.LabelIDs = append([]int(nil), s.LabelIDs...)
		}
		w.Items = append(w.Items, fx)
	}

	return w
}

// ItemsInMilestone returns the IDs of seeded items whose milestone matches
// the named handle. Empty slice if none. Result is sorted ascending.
func (w *World) ItemsInMilestone(name string) []int {
	var mid int
	switch name {
	case "Q1":
		mid = w.Milestones.Q1.ID
	case "Q2":
		mid = w.Milestones.Q2.ID
	case "Backlog":
		mid = w.Milestones.Backlog.ID
	case "Released":
		mid = w.Milestones.Released.ID
	default:
		return nil
	}
	var out []int
	for _, it := range w.Items {
		if it.MilestoneID == mid {
			out = append(out, it.ID)
		}
	}
	sort.Ints(out)
	return out
}

// ItemsInIteration returns the IDs of seeded items in the named iteration.
func (w *World) ItemsInIteration(name string) []int {
	var iid int
	switch name {
	case "Sprint1":
		iid = w.Iterations.Sprint1.ID
	case "Sprint2":
		iid = w.Iterations.Sprint2.ID
	case "Sprint3":
		iid = w.Iterations.Sprint3.ID
	default:
		return nil
	}
	var out []int
	for _, it := range w.Items {
		if it.IterationID == iid {
			out = append(out, it.ID)
		}
	}
	sort.Ints(out)
	return out
}

// ItemsByStatusName returns IDs of seeded items in the named status. The name
// is the symbolic handle ("Open", "InProgress", "InReview", "Done",
// "Canceled"); only Alpha items have status (Beta items are seeded with
// whatever the workspace default is).
func (w *World) ItemsByStatusName(name string) []int {
	var out []int
	for _, it := range w.Items {
		if it.StatusName == name {
			out = append(out, it.ID)
		}
	}
	sort.Ints(out)
	return out
}

// ItemsByAssignee returns IDs of seeded items assigned to userID.
func (w *World) ItemsByAssignee(userID int) []int {
	var out []int
	for _, it := range w.Items {
		if it.AssigneeID == userID {
			out = append(out, it.ID)
		}
	}
	sort.Ints(out)
	return out
}

// ItemsByLabel returns IDs of seeded items carrying labelID.
func (w *World) ItemsByLabel(labelID int) []int {
	var out []int
	for _, it := range w.Items {
		for _, lid := range it.LabelIDs {
			if lid == labelID {
				out = append(out, it.ID)
				break
			}
		}
	}
	sort.Ints(out)
	return out
}

// ItemsInWorkspace returns IDs of seeded items in the given workspace.
func (w *World) ItemsInWorkspace(workspaceID int) []int {
	var out []int
	for _, it := range w.Items {
		if it.WorkspaceID == workspaceID {
			out = append(out, it.ID)
		}
	}
	sort.Ints(out)
	return out
}

// ----- internal seed helpers ------------------------------------------------

func createMilestoneFx(t *testing.T, ts *TestServer, workspaceID int, name, status string) MilestoneFx {
	t.Helper()
	// Workspace-scoped milestones must use the workspace-scoped create route;
	// POST /milestones makes a global milestone regardless of body fields.
	body := map[string]interface{}{
		"name":        name,
		"description": "fixture milestone",
		"status":      status,
	}
	resp := MakeBearerRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/milestones", workspaceID), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create milestone %q: %d - %s", name, resp.StatusCode, string(raw))
	}
	var out struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	DecodeJSON(t, resp, &out)
	return MilestoneFx{ID: out.ID, Name: out.Name}
}

func createIterationFx(t *testing.T, ts *TestServer, workspaceID int, name, status string) IterationFx {
	t.Helper()
	// Provide explicit start/end dates near "now". The list_iterations tool's
	// stale-cutoff filter compares `end_date < now-1y` lexicographically; an
	// empty-string end_date sorts BEFORE any real date and gets dropped, which
	// would make completed seed iterations invisible. Real dates avoid that.
	now := time.Now().UTC()
	body := map[string]interface{}{
		"name":       name,
		"status":     status,
		"start_date": now.AddDate(0, 0, -7).Format("2006-01-02"),
		"end_date":   now.AddDate(0, 0, 7).Format("2006-01-02"),
	}
	resp := MakeBearerRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/iterations", workspaceID), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create iteration %q: %d - %s", name, resp.StatusCode, string(raw))
	}
	var out struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	DecodeJSON(t, resp, &out)
	return IterationFx{ID: out.ID, Name: out.Name, Status: out.Status}
}

func createLabelFx(t *testing.T, ts *TestServer, workspaceID int, name, color string) LabelFx {
	t.Helper()
	body := map[string]interface{}{
		"name":         name,
		"color":        color,
		"workspace_id": workspaceID,
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/labels", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create label %q: %d - %s", name, resp.StatusCode, string(raw))
	}
	var out struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	DecodeJSON(t, resp, &out)
	return LabelFx{ID: out.ID, Name: out.Name}
}

func createItemFx(t *testing.T, ts *TestServer, workspaceID int, title string, statusID, milestoneID, iterationID, assigneeID, itemTypeID int) ItemFx {
	t.Helper()
	body := map[string]interface{}{
		"title":        title,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	}
	if statusID > 0 {
		body["status_id"] = statusID
	}
	if milestoneID > 0 {
		// Items have many milestones via the item_milestones junction; the
		// create payload takes bare ids (itemCreateRequest.MilestoneIDs).
		// The joined `milestones: [{id,...}]` shape is the READ representation.
		body["milestone_ids"] = []int{milestoneID}
	}
	if iterationID > 0 {
		body["iteration_id"] = iterationID
	}
	if assigneeID > 0 {
		body["assignee_id"] = assigneeID
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/items", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create item %q: %d - %s", title, resp.StatusCode, string(raw))
	}
	var out struct {
		ID                  int  `json:"id"`
		WorkspaceItemNumber int  `json:"workspace_item_number"`
		StatusID            *int `json:"status_id"`
	}
	DecodeJSON(t, resp, &out)
	statusFinal := 0
	if out.StatusID != nil {
		statusFinal = *out.StatusID
	} else if statusID > 0 {
		statusFinal = statusID
	}
	wsKey := ""
	for _, candidate := range listWorkspaceKeys(t, ts) {
		if candidate.ID == workspaceID {
			wsKey = candidate.Key
			break
		}
	}
	return ItemFx{
		ID:          out.ID,
		Key:         fmt.Sprintf("%s-%d", wsKey, out.WorkspaceItemNumber),
		WorkspaceID: workspaceID,
		Title:       title,
		StatusID:    statusFinal,
		MilestoneID: milestoneID,
		IterationID: iterationID,
		AssigneeID:  assigneeID,
	}
}

func setItemLabels(t *testing.T, ts *TestServer, itemID int, labelIDs []int) {
	t.Helper()
	body := map[string]interface{}{"label_ids": labelIDs}
	resp := MakeAuthRequest(t, ts, http.MethodPut, fmt.Sprintf("/items/%d/labels", itemID), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("set item %d labels: %d - %s", itemID, resp.StatusCode, string(raw))
	}
}

type wsKeyPair struct {
	ID  int
	Key string
}

func listWorkspaceKeys(t *testing.T, ts *TestServer) []wsKeyPair {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodGet, "/workspaces", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list workspaces: %d", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &arr); err != nil {
		// Workspaces endpoint may return paginated struct. Fall back to that.
		var paged struct {
			Workspaces []map[string]interface{} `json:"workspaces"`
		}
		if jerr := json.Unmarshal(bodyBytes, &paged); jerr != nil {
			t.Fatalf("decode workspaces: %v", err)
		}
		arr = paged.Workspaces
	}
	out := make([]wsKeyPair, 0, len(arr))
	for _, m := range arr {
		idF, _ := m["id"].(float64)
		k, _ := m["key"].(string)
		out = append(out, wsKeyPair{ID: int(idF), Key: k})
	}
	return out
}

func lookupDefaultStatuses(t *testing.T, ts *TestServer, workspaceID int) StatusSetFx {
	t.Helper()
	resp := MakeBearerRequest(t, ts, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/statuses", workspaceID), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("list workspace statuses: %d - %s", resp.StatusCode, string(raw))
	}
	var arr []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	DecodeJSON(t, resp, &arr)
	out := StatusSetFx{}
	for _, s := range arr {
		switch s.Name {
		case "Open":
			out.Open = s.ID
		case "In Progress":
			out.InProgress = s.ID
		case "Done":
			out.Done = s.ID
		}
	}
	if out.Open == 0 || out.InProgress == 0 || out.Done == 0 {
		t.Fatalf("workspace %d missing default statuses (got %+v from %v)", workspaceID, out, arr)
	}
	return out
}

func lookupAdminUser(t *testing.T, ts *TestServer) UserFx {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodGet, "/users", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list users: %d", resp.StatusCode)
	}
	var users []map[string]interface{}
	DecodeJSON(t, resp, &users)
	for _, u := range users {
		if u["username"] == "admin" {
			idF, _ := u["id"].(float64)
			return UserFx{ID: int(idF), Username: "admin"}
		}
	}
	if len(users) > 0 {
		idF, _ := users[0]["id"].(float64)
		uname, _ := users[0]["username"].(string)
		return UserFx{ID: int(idF), Username: uname}
	}
	t.Fatal("no users found")
	return UserFx{}
}

// idsFromJSONListItems extracts the .id field from every entry in a list
// response, sorted ascending. The CLI emits the v1 paginated envelope
// `{"data":[...],"pagination":{...}}` for items; some other endpoints
// return a bare array. We pick by leading byte.
func idsFromJSONListItems(raw []byte) ([]int, error) {
	trimmed := bytesTrimLeading(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var paged struct {
			Data []struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &paged); err != nil {
			return nil, err
		}
		out := make([]int, len(paged.Data))
		for i, e := range paged.Data {
			out[i] = e.ID
		}
		sort.Ints(out)
		return out, nil
	}
	var arr []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	out := make([]int, len(arr))
	for i, e := range arr {
		out[i] = e.ID
	}
	sort.Ints(out)
	return out, nil
}

func bytesTrimLeading(b []byte) []byte {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return b[i:]
		}
	}
	return nil
}
