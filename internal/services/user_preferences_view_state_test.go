package services

import (
	"errors"
	"strings"
	"testing"
)

func TestViewStateMergeAndDelete(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	snapshot, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
		ViewStateSet: true,
		ViewState: map[string]map[string]any{
			"collection-5": {"board": map[string]any{"sort_mode": "bubble"}},
			"workspace-3":  {"map": map[string]any{"swimlane_dimension": "status"}},
		},
	})
	if err != nil {
		t.Fatalf("seed view state: %v", err)
	}
	board, ok := snapshot.ViewState["collection-5"]["board"].(map[string]any)
	if !ok || board["sort_mode"] != "bubble" {
		t.Fatalf("board state = %#v", snapshot.ViewState["collection-5"]["board"])
	}

	// A later patch merges a new view into the same scope without dropping the
	// existing view.
	snapshot, err = svc.UpdateSnapshot(1, UserPreferencesPatch{
		ViewStateSet: true,
		ViewState: map[string]map[string]any{
			"collection-5": {"backlog": map[string]any{"show_completed": true}},
		},
	})
	if err != nil {
		t.Fatalf("merge view: %v", err)
	}
	if _, ok := snapshot.ViewState["collection-5"]["board"]; !ok {
		t.Fatal("existing board view was dropped by the merge")
	}
	backlog, ok := snapshot.ViewState["collection-5"]["backlog"].(map[string]any)
	if !ok || backlog["show_completed"] != true {
		t.Fatalf("backlog state = %#v", snapshot.ViewState["collection-5"]["backlog"])
	}

	// A null view value deletes just that view.
	snapshot, err = svc.UpdateSnapshot(1, UserPreferencesPatch{
		ViewStateSet: true,
		ViewState: map[string]map[string]any{
			"collection-5": {"board": nil},
		},
	})
	if err != nil {
		t.Fatalf("delete view: %v", err)
	}
	if _, ok := snapshot.ViewState["collection-5"]["board"]; ok {
		t.Fatal("board view was not deleted")
	}
	if _, ok := snapshot.ViewState["collection-5"]["backlog"]; !ok {
		t.Fatal("sibling view was deleted with the target")
	}

	// A null scope value deletes the whole scope.
	snapshot, err = svc.UpdateSnapshot(1, UserPreferencesPatch{
		ViewStateSet: true,
		ViewState: map[string]map[string]any{
			"collection-5": nil,
		},
	})
	if err != nil {
		t.Fatalf("delete scope: %v", err)
	}
	if _, ok := snapshot.ViewState["collection-5"]; ok {
		t.Fatal("scope was not deleted")
	}
	if _, ok := snapshot.ViewState["workspace-3"]; !ok {
		t.Fatal("unrelated scope was deleted")
	}

	// Omitting the field leaves the stored state untouched.
	snapshot, err = svc.UpdateSnapshot(1, UserPreferencesPatch{TUIThemeSet: true, TUITheme: "x"})
	if err != nil {
		t.Fatalf("unrelated patch: %v", err)
	}
	if _, ok := snapshot.ViewState["workspace-3"]; !ok {
		t.Fatal("omitted view_state clobbered stored state")
	}
}

func TestSavedFiltersReplaceAndDelete(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	filters := []any{map[string]any{"id": "a", "name": "Mine", "ql": "id > 0"}}
	snapshot, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
		SavedFiltersSet: true,
		SavedFilters: map[string]any{
			"collection-5": filters,
			"workspace-3":  []any{},
		},
	})
	if err != nil {
		t.Fatalf("seed saved filters: %v", err)
	}
	stored, ok := snapshot.SavedFilters["collection-5"].([]any)
	if !ok || len(stored) != 1 {
		t.Fatalf("collection filters = %#v", snapshot.SavedFilters["collection-5"])
	}
	first, _ := stored[0].(map[string]any)
	if first["name"] != "Mine" || first["ql"] != "id > 0" {
		t.Fatalf("collection filter = %#v", first)
	}

	// Replacing one scope keeps the other.
	snapshot, err = svc.UpdateSnapshot(1, UserPreferencesPatch{
		SavedFiltersSet: true,
		SavedFilters:    map[string]any{"collection-5": []any{}},
	})
	if err != nil {
		t.Fatalf("replace filters: %v", err)
	}
	if replaced, ok := snapshot.SavedFilters["collection-5"].([]any); !ok || len(replaced) != 0 {
		t.Fatalf("replaced filters = %#v", snapshot.SavedFilters["collection-5"])
	}
	if _, ok := snapshot.SavedFilters["workspace-3"]; !ok {
		t.Fatal("unrelated scope was dropped")
	}

	// Null deletes the scope.
	snapshot, err = svc.UpdateSnapshot(1, UserPreferencesPatch{
		SavedFiltersSet: true,
		SavedFilters:    map[string]any{"collection-5": nil},
	})
	if err != nil {
		t.Fatalf("delete filters: %v", err)
	}
	if _, ok := snapshot.SavedFilters["collection-5"]; ok {
		t.Fatal("filters scope was not deleted")
	}
}

func TestPreferencesSizeCapRejectsOversizedViewState(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	huge := strings.Repeat("x", maxPreferencesBytes)
	_, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
		ViewStateSet: true,
		ViewState: map[string]map[string]any{
			"collection-1": {"board": huge},
		},
	})
	if !errors.Is(err, ErrPreferencesTooLarge) {
		t.Fatalf("oversized update error = %v, want ErrPreferencesTooLarge", err)
	}
}
