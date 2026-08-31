package handlers

import "testing"

func TestActionURLPointsToItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		actionURL string
		itemID    int
		want      bool
	}{
		{name: "canonical item URL", actionURL: "/workspaces/2/items/42", itemID: 42, want: true},
		{name: "query boundary", actionURL: "/workspaces/2/items/42?tab=comments", itemID: 42, want: true},
		{name: "fragment boundary", actionURL: "/workspaces/2/items/42#comments", itemID: 42, want: true},
		{name: "path boundary", actionURL: "/workspaces/2/items/42/comments", itemID: 42, want: true},
		{name: "sibling item id", actionURL: "/workspaces/2/items/420", itemID: 42, want: false},
		{name: "non route boundary", actionURL: "/workspaces/2/items/42abc", itemID: 42, want: false},
		{name: "different item", actionURL: "/workspaces/2/items/99", itemID: 42, want: false},
		{name: "not an item URL", actionURL: "/workspaces/2", itemID: 42, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := actionURLPointsToItem(tt.actionURL, tt.itemID); got != tt.want {
				t.Fatalf("actionURLPointsToItem(%q, %d) = %v, want %v", tt.actionURL, tt.itemID, got, tt.want)
			}
		})
	}
}
