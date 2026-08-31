package board

import (
	"testing"

	"windshift/internal/tui/data"
)

func TestFilterMatch(t *testing.T) {
	it := &data.WorkItem{ID: 42, Title: "Fix the rudder assembly", AssigneeName: "Ada Lovelace"}

	cases := []struct {
		query string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"rudder", true},
		{"RUDDER", true},
		{"wi-42", true}, // key match via workspace key
		{"ada", true},   // assignee match
		{"fxrdr", true}, // fuzzy fallback
		{"zzz-nope", false},
	}
	for _, c := range cases {
		f := Filter{Query: c.query}
		if got := f.Match(it, "WI"); got != c.want {
			t.Errorf("Filter{%q}.Match() = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestBuildRowsFilterPrunesAndHidesEmptyGroups(t *testing.T) {
	g := testGrouping()
	g.WorkspaceKey = "WI"
	g.Filter = Filter{Query: "mine"}
	rows := BuildRows(testItems(), g)

	var headers []string
	items := 0
	for _, r := range rows {
		if r.Kind == RowHeader {
			headers = append(headers, r.GroupKey)
			if r.GroupKey == "In Progress" && (r.Shown != 1 || r.Count != 2) {
				t.Fatalf("In Progress header shown/count = %d/%d, want 1/2", r.Shown, r.Count)
			}
		} else {
			items++
			if r.Item.Title != "in progress mine" {
				t.Fatalf("unexpected surviving item %q", r.Item.Title)
			}
		}
	}
	if items != 1 {
		t.Fatalf("filter left %d items, want 1", items)
	}
	if len(headers) != 1 || headers[0] != "In Progress" {
		t.Fatalf("empty groups not hidden: headers = %v", headers)
	}
}
