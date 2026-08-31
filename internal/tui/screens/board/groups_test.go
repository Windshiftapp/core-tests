package board

import (
	"testing"

	"windshift/internal/tui/data"
)

func intp(v int) *int { return &v }

func testGrouping() Grouping {
	return Grouping{
		CategoryByStatusID: map[int]string{
			1: "To Do",
			2: "In Progress",
			3: "Done",
		},
		PriorityRank: map[int]int{10: 0, 11: 1},
		MeUserID:     7,
		Collapsed:    map[string]bool{},
	}
}

func testItems() []data.WorkItem {
	return []data.WorkItem{
		{ID: 1, Title: "open old", StatusID: intp(1), UpdatedAt: "2026-01-01T00:00:00Z"},
		{ID: 2, Title: "done", StatusID: intp(3), UpdatedAt: "2026-03-01T00:00:00Z"},
		{ID: 3, Title: "in progress theirs", StatusID: intp(2), AssigneeID: intp(9), UpdatedAt: "2026-03-02T00:00:00Z"},
		{ID: 4, Title: "in progress mine", StatusID: intp(2), AssigneeID: intp(7), UpdatedAt: "2026-01-05T00:00:00Z"},
		{ID: 5, Title: "open new high prio", StatusID: intp(1), PriorityID: intp(10), UpdatedAt: "2026-02-01T00:00:00Z"},
		{ID: 6, Title: "no status"},
	}
}

// flatten returns "H:<group>" for headers and "I:<id>" for items.
func flatten(rows []Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Kind == RowHeader {
			out = append(out, "H:"+r.GroupKey)
		} else {
			out = append(out, "I:"+itoa(r.Item.ID))
		}
	}
	return out
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestBuildRowsGroupOrderAndSorting(t *testing.T) {
	rows := BuildRows(testItems(), testGrouping())
	got := flatten(rows)
	want := []string{
		// In Progress first; mine (4) floats over theirs (3) despite older update
		"H:In Progress", "I:4", "I:3",
		// Middle rank keeps first-seen group order: To Do then No status
		"H:To Do", "I:5", "I:1", // priority 10 beats no-priority
		"H:No status", "I:6",
		// Done last
		"H:Done", "I:2",
	}
	if len(got) != len(want) {
		t.Fatalf("BuildRows produced %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: got %q, want %q\nfull: %v", i, got[i], want[i], got)
		}
	}
}

func TestBuildRowsCollapsedGroupHidesItems(t *testing.T) {
	g := testGrouping()
	g.Collapsed["In Progress"] = true
	rows := BuildRows(testItems(), g)

	for _, r := range rows {
		if r.Kind == RowItem && r.GroupKey == "In Progress" {
			t.Fatalf("collapsed group still emitted item %d", r.Item.ID)
		}
		if r.Kind == RowHeader && r.GroupKey == "In Progress" {
			if !r.Collapsed {
				t.Fatal("header of collapsed group not marked collapsed")
			}
			if r.Count != 2 {
				t.Fatalf("collapsed header count = %d, want 2", r.Count)
			}
		}
	}
}

func TestBuildRowsHeaderCounts(t *testing.T) {
	rows := BuildRows(testItems(), testGrouping())
	counts := map[string]int{}
	for _, r := range rows {
		if r.Kind == RowHeader {
			counts[r.GroupKey] = r.Count
		}
	}
	want := map[string]int{"In Progress": 2, "To Do": 2, "No status": 1, "Done": 1}
	for k, v := range want {
		if counts[k] != v {
			t.Fatalf("group %q count = %d, want %d (all: %v)", k, counts[k], v, counts)
		}
	}
}

func TestBuildRowsEmpty(t *testing.T) {
	if rows := BuildRows(nil, testGrouping()); len(rows) != 0 {
		t.Fatalf("BuildRows(nil) = %d rows, want 0", len(rows))
	}
}

func TestCategoryRank(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"In Progress", 0},
		{"In Review", 0},
		{"To Do", 1},
		{"Backlog", 1},
		{"No status", 1},
		{"Done", 2},
		{"Completed", 2},
		{"Cancelled", 2},
	}
	for _, c := range cases {
		if got := categoryRank(c.name); got != c.want {
			t.Errorf("categoryRank(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}
