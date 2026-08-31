//go:build test

package dialog

import (
	"fmt"
	"strings"
	"testing"

	"windshift/internal/tui/styles"
)

func TestPickerShowsSearchAndBoundsLargeResultSets(t *testing.T) {
	options := make([]Option, 100)
	for i := range options {
		name := fmt.Sprintf("Person %03d", i)
		options[i] = Option{Label: name, Search: name, Value: i}
	}
	picker := NewPicker("assignee", "Assign to", options, 0, styles.New(styles.CatppuccinMocha()))
	view := picker.View(50, 8)
	if !strings.Contains(view, "type a name") || !strings.Contains(view, "100/100") {
		t.Fatalf("picker does not expose its search affordance: %q", view)
	}
	if strings.Contains(view, "Person 099") {
		t.Fatal("picker rendered all results instead of a bounded window")
	}

	picker.query = "Person 099"
	picker.applyFilter()
	view = picker.View(50, 8)
	if !strings.Contains(view, "Person 099") || !strings.Contains(view, "1/100") {
		t.Fatalf("picker did not filter the large result set: %q", view)
	}
}
