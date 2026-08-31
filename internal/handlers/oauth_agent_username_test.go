package handlers

import (
	"testing"
	"unicode/utf8"
)

func TestOAuthAgentUsername(t *testing.T) {
	tests := []struct {
		name     string
		clientID int
		slug     string
		userID   int
		want     string
	}{
		{
			name:     "short slug preserves readable convention",
			clientID: 17,
			slug:     "calendar",
			userID:   4,
			want:     "oauth-calendar-4",
		},
		{
			name:     "long slug uses unique bounded IDs",
			clientID: 17,
			slug:     "browser-client-with-a-deliberately-long-slug",
			userID:   4,
			want:     "oauth-17-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oauthAgentUsername(tt.clientID, tt.slug, tt.userID)
			if got != tt.want {
				t.Fatalf("oauthAgentUsername() = %q, want %q", got, tt.want)
			}
			if utf8.RuneCountInString(got) > maxOAuthAgentUsername {
				t.Fatalf("oauthAgentUsername() length = %d, max %d", utf8.RuneCountInString(got), maxOAuthAgentUsername)
			}
		})
	}
}
