package services

import "testing"

func TestMobileActionURL(t *testing.T) {
	tests := []struct {
		name      string
		actionURL string
		want      string
	}{
		{
			name:      "desktop workspace item deep link rewrites to mobile",
			actionURL: "/workspaces/2/items/481",
			want:      "/m/items/481",
		},
		{
			name:      "nested-collection item deep link rewrites to mobile",
			actionURL: "/workspaces/2/collections/9/items/481",
			want:      "/m/items/481",
		},
		{
			name:      "item link with trailing query string still rewrites",
			actionURL: "/workspaces/2/items/481?scroll=comments",
			want:      "/m/items/481",
		},
		{
			name:      "empty action url stays empty so caller falls back",
			actionURL: "",
			want:      "",
		},
		{
			name:      "non-item url is passed through unchanged",
			actionURL: "/m/something-else",
			want:      "/m/something-else",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mobileActionURL(tt.actionURL); got != tt.want {
				t.Fatalf("mobileActionURL(%q) = %q, want %q", tt.actionURL, got, tt.want)
			}
		})
	}
}
