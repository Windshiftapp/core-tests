// wscli_smoke_test verifies the new fixture seed and the in-process ws CLI
// entry point both work end-to-end against an isolated test server. It is the
// minimum bar before the per-command rigorous suites (wscli_task_test,
// wscli_milestone_test, wscli_diagram_test, etc.) are layered on top.
package tests

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"windshift/internal/wscli"
)

// TestWSCLI_Smoke_SeedAndListItems is the canary for the whole stack:
//   - SeedWorld creates the deterministic dataset
//   - wscli.Run exercises the in-process Cobra tree
//   - the CLI hits the same /api/items endpoint as the rest of the suite
//   - JSON output matches the SeedWorld expectations
//
// Failures here mean the refactor or fixtures need attention before we trust
// any of the per-command tests.
func TestWSCLI_Smoke_SeedAndListItems(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)

	w := SeedWorld(t, ts)

	// Sanity: the fixture-side accounting must agree with what the API
	// returns. `GET /api/items?workspace_id=` is the auth-side, paged
	// response we'd hit before MCP/CLI; wire up against it first.
	gotPaged := GetItemsByWorkspace(t, ts, w.Alpha.ID)
	if len(gotPaged) != len(w.ItemsInWorkspace(w.Alpha.ID)) {
		t.Fatalf("seed sanity: GET /items returned %d, fixture has %d", len(gotPaged), len(w.ItemsInWorkspace(w.Alpha.ID)))
	}

	// Now exercise the CLI itself.
	var stdout, stderr bytes.Buffer
	code := wscli.Run(
		context.Background(),
		[]string{"task", "ls", "-w", w.Alpha.Key, "-o", "json"},
		nil, &stdout, &stderr,
		map[string]string{
			"WS_URL":   ts.BaseURL,
			"WS_TOKEN": ts.BearerToken,
		},
	)
	if code != 0 {
		t.Fatalf("ws task ls returned %d, stderr=%s", code, stderr.String())
	}

	gotIDs, err := idsFromJSONListItems(stdout.Bytes())
	if err != nil {
		t.Fatalf("decode CLI output: %v\nraw=%s", err, stdout.String())
	}
	wantIDs := w.ItemsInWorkspace(w.Alpha.ID)
	sort.Ints(wantIDs)
	if !equalIntSlices(gotIDs, wantIDs) {
		t.Fatalf("ws task ls returned %v, want %v", gotIDs, wantIDs)
	}
}

// TestWSCLI_Smoke_FilterByAssignee proves that filter-correctness assertions
// against the seed work via the CLI. `task ls --assignee N` must equal the
// seed's expected set for the same assignee.
//
// Note: CLI `task ls` supports --status / --assignee / --type / --priority
// natively, but not --milestone or --iteration. The rigorous suite exercises
// those filters via the MCP `list_items` tool (which speaks CQL) and the
// dedicated `milestone get --progress` / `iteration` endpoints.
func TestWSCLI_Smoke_FilterByAssignee(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	var stdout, stderr bytes.Buffer
	code := wscli.Run(
		context.Background(),
		[]string{"task", "ls", "-w", w.Alpha.Key, "--assignee", itoa(w.Users.Alice.ID), "-o", "json"},
		nil, &stdout, &stderr,
		map[string]string{"WS_URL": ts.BaseURL, "WS_TOKEN": ts.BearerToken},
	)
	if code != 0 {
		t.Fatalf("ws task ls --assignee: %d, stderr=%s", code, stderr.String())
	}
	gotIDs, err := idsFromJSONListItems(stdout.Bytes())
	if err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, stdout.String())
	}
	// SeedWorld put Alice on items in both Alpha and Beta. Alpha-only filter
	// kicks in via -w; we expect Alice's Alpha-only items.
	wantIDs := intersectInts(w.ItemsByAssignee(w.Users.Alice.ID), w.ItemsInWorkspace(w.Alpha.ID))
	sort.Ints(wantIDs)
	if !equalIntSlices(gotIDs, wantIDs) {
		t.Fatalf("assignee filter: got %v, want %v", gotIDs, wantIDs)
	}
}

// intersectInts returns sorted ascending intersection of two sorted slices.
func intersectInts(a, b []int) []int {
	set := make(map[int]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	var out []int
	for _, v := range a {
		if _, ok := set[v]; ok {
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// TestWSCLI_Smoke_StatusFilter exercises the negation operator, since that
// is the easiest filter the CLI exposes natively (--status ~done). Every
// item not in the Done status should appear in the result.
func TestWSCLI_Smoke_StatusFilter(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	var stdout, stderr bytes.Buffer
	code := wscli.Run(
		context.Background(),
		[]string{"task", "ls", "-w", w.Alpha.Key, "-s", itoa(w.Statuses.Done), "-o", "json"},
		nil, &stdout, &stderr,
		map[string]string{"WS_URL": ts.BaseURL, "WS_TOKEN": ts.BearerToken},
	)
	if code != 0 {
		t.Fatalf("ws task ls --status: %d, stderr=%s", code, stderr.String())
	}
	gotIDs, err := idsFromJSONListItems(stdout.Bytes())
	if err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, stdout.String())
	}
	wantIDs := w.ItemsByStatusName("Done")
	sort.Ints(wantIDs)
	if !equalIntSlices(gotIDs, wantIDs) {
		t.Fatalf("status filter: got %v, want %v", gotIDs, wantIDs)
	}
}

// equalIntSlices returns true if both slices contain the same elements in
// the same order. Both inputs are expected to be sorted ascending.
func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(i int) string {
	// avoid importing strconv just for this in a single test file
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
