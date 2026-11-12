package services

import (
	"errors"
	"testing"
)

// TestCommentSortOrderPreferencesRoundTrip pins the per-user comment thread
// order: a fresh user defaults to oldest-first, a patch persists newest-first,
// and unrelated preferences survive the write.
func TestCommentSortOrderPreferencesRoundTrip(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	fresh, err := svc.GetSnapshot(1)
	if err != nil {
		t.Fatalf("fresh snapshot: %v", err)
	}
	if fresh.CommentSortOrder != CommentSortOldest {
		t.Fatalf("fresh comment sort order = %q, want %q", fresh.CommentSortOrder, CommentSortOldest)
	}

	updated, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
		CommentSortOrderSet: true,
		CommentSortOrder:    CommentSortNewest,
	})
	if err != nil {
		t.Fatalf("update comment sort order: %v", err)
	}
	if updated.CommentSortOrder != CommentSortNewest {
		t.Fatalf("updated comment sort order = %q, want %q", updated.CommentSortOrder, CommentSortNewest)
	}

	// A later patch that omits the field must not reset it.
	unchanged, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
		ViewStateSet: true,
		ViewState:    map[string]map[string]any{"collection-5": {"board": map[string]any{"sort_mode": "bubble"}}},
	})
	if err != nil {
		t.Fatalf("unrelated update: %v", err)
	}
	if unchanged.CommentSortOrder != CommentSortNewest {
		t.Fatalf("comment sort order after unrelated patch = %q, want %q", unchanged.CommentSortOrder, CommentSortNewest)
	}

	reloaded, err := svc.GetSnapshot(1)
	if err != nil {
		t.Fatalf("reload snapshot: %v", err)
	}
	if reloaded.CommentSortOrder != CommentSortNewest {
		t.Fatalf("reloaded comment sort order = %q, want %q", reloaded.CommentSortOrder, CommentSortNewest)
	}
}

func TestCommentSortOrderPreferencesRejectUnknownOrder(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	for _, order := range []string{"", "OLDEST", "recent", "newest-first"} {
		_, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
			CommentSortOrderSet: true,
			CommentSortOrder:    order,
		})
		if !errors.Is(err, ErrInvalidCommentSortOrder) {
			t.Fatalf("order=%q error = %v, want ErrInvalidCommentSortOrder", order, err)
		}
	}
}
