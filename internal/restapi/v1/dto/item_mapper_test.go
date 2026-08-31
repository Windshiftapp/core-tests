//go:build test

package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"windshift/internal/models"
)

func TestItemResponseDescriptionHTMLScope(t *testing.T) {
	item := models.Item{
		ID:                  42,
		WorkspaceID:         7,
		WorkspaceKey:        "WI",
		WorkspaceItemNumber: 215,
		Title:               "Preserve Markdown",
		Description:         "`Promise<Anything>`",
	}

	t.Run("single item includes rendered description", func(t *testing.T) {
		response := MapItemToResponse(&item, "")
		if !strings.Contains(response.DescriptionHTML, "Promise&lt;Anything&gt;") {
			t.Fatalf("description_html = %q, want rendered inline code", response.DescriptionHTML)
		}
	})

	t.Run("item collection omits rendered description", func(t *testing.T) {
		responses := MapItemsToResponse([]models.Item{item}, "")
		if len(responses) != 1 {
			t.Fatalf("response count = %d, want 1", len(responses))
		}
		if responses[0].Description != item.Description {
			t.Fatalf("description = %q, want exact source %q", responses[0].Description, item.Description)
		}
		if responses[0].DescriptionHTML != "" {
			t.Fatalf("description_html = %q, want empty", responses[0].DescriptionHTML)
		}

		payload, err := json.Marshal(responses)
		if err != nil {
			t.Fatalf("marshal responses: %v", err)
		}
		if strings.Contains(string(payload), `"description_html"`) {
			t.Fatalf("list payload includes description_html: %s", payload)
		}
	})
}

func TestCommentResponseOmitsEmptyContentHTML(t *testing.T) {
	payload, err := json.Marshal(CommentResponse{})
	if err != nil {
		t.Fatalf("marshal comment response: %v", err)
	}
	if strings.Contains(string(payload), `"content_html"`) {
		t.Fatalf("empty comment payload includes content_html: %s", payload)
	}
}
