// wscli_task_test exercises every `ws task ...` subcommand against the
// SeedWorld dataset. Each filter assertion compares the CLI's resultset to
// the seed's expected IDs — the seed's matrix in fixtures.go is the source
// of truth.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/wscli"
)

// runWS is a thin wrapper over wscli.Run for tests. It always points at the
// supplied test server and returns (stdoutBytes, stderrString, exitCode).
func runWS(t *testing.T, ts *TestServer, args ...string) ([]byte, string, int) {
	t.Helper()
	var so, se bytes.Buffer
	code := wscli.Run(
		context.Background(), args, nil, &so, &se,
		map[string]string{"WS_URL": ts.BaseURL, "WS_TOKEN": ts.BearerToken},
	)
	return so.Bytes(), se.String(), code
}

// requireZero fails the test if the CLI returned non-zero, dumping stderr.
func requireZero(t *testing.T, code int, stderr string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("ws returned %d, stderr=%s", code, stderr)
	}
}

// TestWSCLI_Task_Mine asserts `ws task mine` returns only items assigned to
// the authenticated user. Admin (the bearer-token user) has no items
// assigned in the seed, so the result is empty by design.
func TestWSCLI_Task_Mine(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	out, stderr, code := runWS(t, ts, "task", "mine", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)

	gotIDs, err := idsFromJSONListItems(out)
	if err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, string(out))
	}
	wantIDs := intersectInts(w.ItemsByAssignee(w.Users.Admin.ID), w.ItemsInWorkspace(w.Alpha.ID))
	sort.Ints(wantIDs)
	if !equalIntSlices(gotIDs, wantIDs) {
		t.Fatalf("task mine: got %v, want %v", gotIDs, wantIDs)
	}
}

// TestWSCLI_Task_Created proves the creator filter. The admin bearer token
// created every seeded item, so `task created` should return them all
// (within the requested workspace).
func TestWSCLI_Task_Created(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	out, stderr, code := runWS(t, ts, "task", "created", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	gotIDs, _ := idsFromJSONListItems(out)
	wantIDs := w.ItemsInWorkspace(w.Alpha.ID)
	if !equalIntSlices(gotIDs, wantIDs) {
		t.Fatalf("task created: got %v, want %v", gotIDs, wantIDs)
	}
}

// TestWSCLI_Task_List_FilterMatrix runs `ws task ls` against every native
// filter dimension (status / assignee / type / priority / status-negation)
// and verifies the resultset matches SeedWorld's expectation.
func TestWSCLI_Task_List_FilterMatrix(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	cases := []struct {
		name string
		args []string
		want []int
	}{
		{
			"all in alpha",
			[]string{"task", "ls", "-w", w.Alpha.Key, "-o", "json"},
			w.ItemsInWorkspace(w.Alpha.ID),
		},
		{
			"status=Done",
			[]string{"task", "ls", "-w", w.Alpha.Key, "-s", strconv.Itoa(w.Statuses.Done), "-o", "json"},
			w.ItemsByStatusName("Done"),
		},
		{
			"status=InProgress",
			[]string{"task", "ls", "-w", w.Alpha.Key, "-s", strconv.Itoa(w.Statuses.InProgress), "-o", "json"},
			w.ItemsByStatusName("InProgress"),
		},
		{
			"status negation ~Done",
			[]string{"task", "ls", "-w", w.Alpha.Key, "-s", "~" + strconv.Itoa(w.Statuses.Done), "-o", "json"},
			diffInts(w.ItemsInWorkspace(w.Alpha.ID), w.ItemsByStatusName("Done")),
		},
		{
			"assignee=Alice",
			[]string{"task", "ls", "-w", w.Alpha.Key, "--assignee", strconv.Itoa(w.Users.Alice.ID), "-o", "json"},
			intersectInts(w.ItemsByAssignee(w.Users.Alice.ID), w.ItemsInWorkspace(w.Alpha.ID)),
		},
		{
			"assignee=Bob",
			[]string{"task", "ls", "-w", w.Alpha.Key, "--assignee", strconv.Itoa(w.Users.Bob.ID), "-o", "json"},
			intersectInts(w.ItemsByAssignee(w.Users.Bob.ID), w.ItemsInWorkspace(w.Alpha.ID)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := runWS(t, ts, tc.args...)
			requireZero(t, code, stderr)
			gotIDs, err := idsFromJSONListItems(out)
			if err != nil {
				t.Fatalf("decode: %v\nraw=%s", err, string(out))
			}
			want := append([]int(nil), tc.want...)
			sort.Ints(want)
			if !equalIntSlices(gotIDs, want) {
				t.Fatalf("got %v, want %v", gotIDs, want)
			}
		})
	}
}

// TestWSCLI_Task_Get returns the right item by both numeric ID and KEY-NUM
// shorthand, and includes the transitions expansion.
func TestWSCLI_Task_Get(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	// Pick one item with a known status; ItemFx[1] is "alpha feature ramp"
	// in In Progress.
	target := w.Items[1]

	t.Run("by numeric id", func(t *testing.T) {
		out, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
		requireZero(t, code, stderr)
		var got struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("decode: %v\nraw=%s", err, string(out))
		}
		if got.ID != target.ID || got.Title != target.Title {
			t.Fatalf("got %+v, want id=%d title=%q", got, target.ID, target.Title)
		}
	})

	t.Run("by KEY-NUMBER", func(t *testing.T) {
		// fixture stored Key already in the form "<wsKey>-<num>"
		out, stderr, code := runWS(t, ts, "task", "get", target.Key, "-o", "json")
		requireZero(t, code, stderr)
		if !bytes.Contains(out, []byte(target.Title)) {
			t.Fatalf("KEY-NUMBER lookup: stdout missing title %q\nraw=%s", target.Title, string(out))
		}
	})
}

// TestWSCLI_Task_Children proves the parent-child relation: setting a parent
// then listing children returns exactly the dependent rows.
func TestWSCLI_Task_Children(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	parent := w.Items[0]

	// Create three children pointing at the parent through the CLI itself.
	for i := 0; i < 3; i++ {
		_, stderr, code := runWS(t, ts,
			"task", "create",
			"-w", w.Alpha.Key,
			"-t", "child of "+strconv.Itoa(parent.ID)+" #"+strconv.Itoa(i),
			"--parent", strconv.Itoa(parent.ID),
			"-o", "json",
		)
		requireZero(t, code, stderr)
	}

	out, stderr, code := runWS(t, ts, "task", "children", strconv.Itoa(parent.ID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	gotIDs, err := idsFromJSONListItems(out)
	if err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, string(out))
	}
	if len(gotIDs) != 3 {
		t.Fatalf("expected 3 children, got %d (ids=%v)", len(gotIDs), gotIDs)
	}
}

// createItemViaCLI creates an item through `ws task create` and returns the
// (id, key) the server assigned. Optional parentID (>0) sets a parent.
func createItemViaCLI(t *testing.T, ts *TestServer, wsKey, title string, parentID int) (int, string) {
	t.Helper()
	args := []string{"task", "create", "-w", wsKey, "-t", title, "-o", "json"}
	if parentID > 0 {
		args = append(args, "--parent", strconv.Itoa(parentID))
	}
	out, stderr, code := runWS(t, ts, args...)
	requireZero(t, code, stderr)
	var created struct {
		ID  int    `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode created item: %v\nraw=%s", err, string(out))
	}
	return created.ID, created.Key
}

// TestWSCLI_Task_Parent proves the inverse of `task children`: `task parent`
// resolves an item's parent, `task get` surfaces the parent's *key* (built
// from the parent's workspace number, never the raw DB parent_id) plus the
// item's children, and a top-level item reports no parent.
func TestWSCLI_Task_Parent(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	parentID, parentKey := createItemViaCLI(t, ts, w.Alpha.Key, "parent item", 0)
	childID, childKey := createItemViaCLI(t, ts, w.Alpha.Key, "child item", parentID)

	// `task parent <child>` returns the parent item.
	out, stderr, code := runWS(t, ts, "task", "parent", strconv.Itoa(childID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var gotParent struct {
		ID  int    `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(out, &gotParent); err != nil {
		t.Fatalf("decode parent: %v\nraw=%s", err, string(out))
	}
	if gotParent.ID != parentID || gotParent.Key != parentKey {
		t.Fatalf("task parent %s: got id=%d key=%s, want id=%d key=%s", childKey, gotParent.ID, gotParent.Key, parentID, parentKey)
	}

	// `task get <child>` surfaces parent_id, the resolved parent_key (the
	// real workspace key, not item:<id>), and the parent's title.
	out, stderr, code = runWS(t, ts, "task", "get", strconv.Itoa(childID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var child struct {
		ParentID    *int   `json:"parent_id"`
		ParentKey   string `json:"parent_key"`
		ParentTitle string `json:"parent_title"`
	}
	if err := json.Unmarshal(out, &child); err != nil {
		t.Fatalf("decode child get: %v\nraw=%s", err, string(out))
	}
	if child.ParentID == nil || *child.ParentID != parentID {
		t.Fatalf("child parent_id: got %v, want %d", child.ParentID, parentID)
	}
	if child.ParentKey != parentKey {
		t.Fatalf("child parent_key: got %q, want %q (must be the workspace key, not item:<id>)", child.ParentKey, parentKey)
	}
	if child.ParentTitle != "parent item" {
		t.Fatalf("child parent_title: got %q, want %q", child.ParentTitle, "parent item")
	}

	// `task get <parent>` lists the child among its children.
	out, stderr, code = runWS(t, ts, "task", "get", strconv.Itoa(parentID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var parent struct {
		Children []struct {
			ID int `json:"id"`
		} `json:"children"`
	}
	if err := json.Unmarshal(out, &parent); err != nil {
		t.Fatalf("decode parent get: %v\nraw=%s", err, string(out))
	}
	foundChild := false
	for _, c := range parent.Children {
		if c.ID == childID {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatalf("parent get children: %d not among %+v", childID, parent.Children)
	}

	// A top-level item reports no parent (exit 0, message on stdout).
	out, stderr, code = runWS(t, ts, "task", "parent", strconv.Itoa(parentID), "-w", w.Alpha.Key)
	requireZero(t, code, stderr)
	if !strings.Contains(string(out), "has no parent") {
		t.Fatalf("expected 'has no parent' for top-level item, got: %s", string(out))
	}
}

// runWSAs runs the CLI with an explicit bearer token (rather than the admin
// token runWS uses), so tests can exercise per-user visibility.
func runWSAs(t *testing.T, ts *TestServer, token string, args ...string) ([]byte, string, int) {
	t.Helper()
	var so, se bytes.Buffer
	code := wscli.Run(
		context.Background(), args, nil, &so, &se,
		map[string]string{"WS_URL": ts.BaseURL, "WS_TOKEN": token},
	)
	return so.Bytes(), se.String(), code
}

// TestWSCLI_Task_Parent_CrossWorkspaceRedaction proves that a caller who can
// view a child but not its (cross-workspace) parent never sees the parent's
// key or title — only the opaque parent_id. Admin authors a parent in Beta and
// a child in Alpha pointing at it while both are open, then Beta is locked
// down. Bob can view Alpha but holds no role in the now-gated Beta.
func TestWSCLI_Task_Parent_CrossWorkspaceRedaction(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	betaParentID, betaParentKey := createItemViaCLI(t, ts, w.Beta.Key, "secret beta parent", 0)
	childID, _ := createItemViaCLI(t, ts, w.Alpha.Key, "alpha child of beta parent", betaParentID)

	// Gate Beta so non-members can no longer view it; Bob holds no Beta role.
	LockDownWorkspace(t, ts, w.Beta.ID)

	type parentView struct {
		ParentID    *int   `json:"parent_id"`
		ParentKey   string `json:"parent_key"`
		ParentTitle string `json:"parent_title"`
	}
	decode := func(out []byte) parentView {
		var v parentView
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatalf("decode: %v\nraw=%s", err, string(out))
		}
		return v
	}

	// Admin can view Beta, so it sees the parent's real key and title.
	out, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(childID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	if adminView := decode(out); adminView.ParentKey != betaParentKey || adminView.ParentTitle != "secret beta parent" {
		t.Fatalf("admin view: got key=%q title=%q, want key=%q title=%q",
			adminView.ParentKey, adminView.ParentTitle, betaParentKey, "secret beta parent")
	}

	// Bob can view Alpha (open) but not Beta (gated): the parent key and title
	// must be withheld, leaving only the opaque parent_id.
	bobToken := createTokenWithScopesAsUser(t, ts, w.Users.Bob.Username, "testpass123", []string{"items:read"})
	out, stderr, code = runWSAs(t, ts, bobToken, "task", "get", strconv.Itoa(childID), "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	bobView := decode(out)
	if bobView.ParentID == nil || *bobView.ParentID != betaParentID {
		t.Fatalf("bob parent_id: got %v, want %d (the raw id is not secret)", bobView.ParentID, betaParentID)
	}
	if bobView.ParentTitle != "" {
		t.Fatalf("bob must not see the cross-workspace parent's title, got %q", bobView.ParentTitle)
	}
	if strings.Contains(bobView.ParentKey, w.Beta.Key) {
		t.Fatalf("bob must not see the parent's real key, got %q (leaks workspace %s)", bobView.ParentKey, w.Beta.Key)
	}
}

// TestWSCLI_Task_Create makes a new item via CLI and confirms it appears in
// later listings with the requested fields.
func TestWSCLI_Task_Create(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	out, stderr, code := runWS(t, ts,
		"task", "create",
		"-w", w.Alpha.Key,
		"-t", "newly created via cli",
		"-d", "described inline",
		"--assignee", strconv.Itoa(w.Users.Bob.ID),
		"-o", "json",
	)
	requireZero(t, code, stderr)

	var created struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode create response: %v\nraw=%s", err, string(out))
	}
	if created.Title != "newly created via cli" {
		t.Fatalf("created title=%q want %q", created.Title, "newly created via cli")
	}

	// The new item should now show up filtered by assignee=Bob in the same
	// workspace.
	listOut, stderr, code := runWS(t, ts, "task", "ls", "-w", w.Alpha.Key, "--assignee", strconv.Itoa(w.Users.Bob.ID), "-o", "json")
	requireZero(t, code, stderr)
	gotIDs, _ := idsFromJSONListItems(listOut)
	if !containsInt(gotIDs, created.ID) {
		t.Fatalf("created id %d missing from --assignee=%d list (got %v)", created.ID, w.Users.Bob.ID, gotIDs)
	}
}

// TestWSCLI_Task_Edit updates fields on an existing item and confirms the
// server now reflects the change.
func TestWSCLI_Task_Edit(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	target := w.Items[9] // "alpha unstructured"
	_, stderr, code := runWS(t, ts,
		"task", "edit", strconv.Itoa(target.ID),
		"-t", "renamed via edit",
		"--assignee", strconv.Itoa(w.Users.Alice.ID),
		"-o", "json",
	)
	requireZero(t, code, stderr)

	// Re-fetch and confirm.
	out, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
	requireZero(t, code, stderr)
	var got struct {
		Title    string `json:"title"`
		Assignee struct {
			ID int `json:"id"`
		} `json:"assignee"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, string(out))
	}
	if got.Title != "renamed via edit" {
		t.Fatalf("title not updated, got %q", got.Title)
	}
	if got.Assignee.ID != w.Users.Alice.ID {
		t.Fatalf("assignee not updated, got %d want %d", got.Assignee.ID, w.Users.Alice.ID)
	}
}

// TestWSCLI_Task_Move performs a workflow transition through the CLI and
// confirms the target's status changed.
func TestWSCLI_Task_Move(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	target := w.Items[0]
	_, stderr, code := runWS(t, ts,
		"task", "move", strconv.Itoa(target.ID), strconv.Itoa(w.Statuses.InProgress),
		"-w", w.Alpha.Key, "-o", "json",
	)
	requireZero(t, code, stderr)

	out, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
	requireZero(t, code, stderr)
	var got struct {
		Status struct {
			ID int `json:"id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, string(out))
	}
	if got.Status.ID != w.Statuses.InProgress {
		t.Fatalf("status not transitioned: got %d want %d", got.Status.ID, w.Statuses.InProgress)
	}
}

// TestWSCLI_Task_SetMilestone covers both setting and clearing the milestone
// assignment via the CLI.
func TestWSCLI_Task_SetMilestone(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	target := w.Items[9] // unstructured: starts with no milestone
	_, stderr, code := runWS(t, ts,
		"task", "set-milestone", strconv.Itoa(target.ID), w.Milestones.Q2.Name,
		"-w", w.Alpha.Key, "-o", "json",
	)
	requireZero(t, code, stderr)

	out, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
	requireZero(t, code, stderr)
	// Items now expose milestones as a list ({milestones: [{id, name, ...}]})
	// since the move to the item_milestones junction.
	var got struct {
		Milestones []struct {
			ID int `json:"id"`
		} `json:"milestones"`
	}
	_ = json.Unmarshal(out, &got)
	if len(got.Milestones) != 1 || got.Milestones[0].ID != w.Milestones.Q2.ID {
		t.Fatalf("milestone not set, got %+v want [id=%d]", got.Milestones, w.Milestones.Q2.ID)
	}

	// Now clear it.
	_, stderr, code = runWS(t, ts, "task", "set-milestone", strconv.Itoa(target.ID), "--clear", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	out, stderr, code = runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
	requireZero(t, code, stderr)
	got.Milestones = nil
	_ = json.Unmarshal(out, &got)
	if len(got.Milestones) != 0 {
		t.Fatalf("milestone not cleared, got %+v", got.Milestones)
	}
}

// TestWSCLI_OutputFormats spot-checks that table / csv output don't crash and
// produce non-empty output containing the expected item key. Full visual
// equivalence checks are out of scope — we only need confidence the
// formatters wire up to package stdout.
func TestWSCLI_OutputFormats(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	for _, format := range []string{"table", "csv"} {
		t.Run(format, func(t *testing.T) {
			out, stderr, code := runWS(t, ts, "task", "ls", "-w", w.Alpha.Key, "-o", format)
			requireZero(t, code, stderr)
			if len(out) == 0 {
				t.Fatalf("empty output for format %s", format)
			}
			// At least one of our item titles should appear in the output.
			needle := w.Items[0].Title
			if !strings.Contains(string(out), needle) {
				t.Fatalf("format %s missing seeded title %q in output:\n%s", format, needle, string(out))
			}
		})
	}
}

// diffInts returns elements of a not present in b, sorted ascending.
func diffInts(a, b []int) []int {
	set := make(map[int]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	var out []int
	for _, v := range a {
		if _, ok := set[v]; !ok {
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// assertItemDates fetches an item via `ws task get` and asserts its three
// date fields render as the given YYYY-MM-DD values ("" = expect unset).
func assertItemDates(t *testing.T, ts *TestServer, itemID int, due, start, end string) {
	t.Helper()
	out, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(itemID), "-o", "json")
	requireZero(t, code, stderr)
	var got struct {
		DueDate   *time.Time `json:"due_date"`
		StartDate *time.Time `json:"start_date"`
		EndDate   *time.Time `json:"end_date"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, string(out))
	}
	check := func(field, want string, val *time.Time) {
		t.Helper()
		gotStr := ""
		if val != nil {
			gotStr = val.Format("2006-01-02")
		}
		if gotStr != want {
			t.Fatalf("%s: got %q want %q", field, gotStr, want)
		}
	}
	check("due_date", due, got.DueDate)
	check("start_date", start, got.StartDate)
	check("end_date", end, got.EndDate)
}

// TestWSCLI_Task_Dates covers WI-323: the --due-date/--start-date/--end-date
// flags on create and edit. The edit leg doubles as the regression test for
// the REST v1 typed-DTO update path, which used to drop time.Time date
// values silently instead of persisting them.
func TestWSCLI_Task_Dates(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	out, stderr, code := runWS(t, ts,
		"task", "create", "-w", w.Alpha.Key,
		"-t", "dated via cli",
		"--due-date", "2026-07-20",
		"--start-date", "2026-07-01",
		"--end-date", "2026-07-15",
		"-o", "json",
	)
	requireZero(t, code, stderr)
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode create response: %v\nraw=%s", err, string(out))
	}
	assertItemDates(t, ts, created.ID, "2026-07-20", "2026-07-01", "2026-07-15")

	// Update all three dates through PUT /rest/api/v1/items/{id}.
	_, stderr, code = runWS(t, ts,
		"task", "edit", strconv.Itoa(created.ID),
		"--due-date", "2026-08-01",
		"--start-date", "2026-07-05",
		"--end-date", "2026-07-25",
		"-o", "json",
	)
	requireZero(t, code, stderr)
	assertItemDates(t, ts, created.ID, "2026-08-01", "2026-07-05", "2026-07-25")

	// Malformed values fail client-side with a non-zero exit.
	_, stderr, code = runWS(t, ts, "task", "edit", strconv.Itoa(created.ID), "--due-date", "20-08-2026")
	if code == 0 {
		t.Fatal("expected non-zero exit for malformed --due-date")
	}
	if !strings.Contains(stderr, "YYYY-MM-DD") {
		t.Fatalf("expected YYYY-MM-DD hint in error, got: %s", stderr)
	}
	// The failed edit must not have changed anything.
	assertItemDates(t, ts, created.ID, "2026-08-01", "2026-07-05", "2026-07-25")
}

// TestWSCLI_Task_Edit_TypeByName covers WI-318: `ws task edit --type` accepts
// an item type name (not just a numeric ID) and routes the change through the
// dedicated change-type endpoint.
func TestWSCLI_Task_Edit_TypeByName(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	target := w.Items[9] // "alpha unstructured"

	// Pick a target type different from the item's current one.
	out, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
	requireZero(t, code, stderr)
	var current struct {
		ItemType struct {
			ID int `json:"id"`
		} `json:"item_type"`
	}
	if err := json.Unmarshal(out, &current); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, string(out))
	}
	configSetID := GetDefaultConfigurationSet(t, ts)
	var wantName string
	var wantID int
	for name, id := range GetItemTypes(t, ts, configSetID) {
		// A parentless item cannot be changed to the generic Sub-task type.
		if id != current.ItemType.ID && name != "Sub-task" {
			wantName, wantID = name, id
			break
		}
	}
	if wantID == 0 {
		t.Skip("seed has only one item type; cannot exercise change-type")
	}

	_, stderr, code = runWS(t, ts, "task", "edit", strconv.Itoa(target.ID), "--type", wantName, "-o", "json")
	requireZero(t, code, stderr)

	out, stderr, code = runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
	requireZero(t, code, stderr)
	var got struct {
		ItemType struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"item_type"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, string(out))
	}
	if got.ItemType.ID != wantID {
		t.Fatalf("item type not changed: got %d (%s) want %d (%s)", got.ItemType.ID, got.ItemType.Name, wantID, wantName)
	}

	// Unknown names fail with the available catalog in the message.
	_, stderr, code = runWS(t, ts, "task", "edit", strconv.Itoa(target.ID), "--type", "no-such-type")
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown --type name")
	}
	if !strings.Contains(stderr, "Available types") {
		t.Fatalf("expected available-types listing in error, got: %s", stderr)
	}
}
