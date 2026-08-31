package data

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientGetCommentsFollowsAllPages(t *testing.T) {
	requestedPages := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/v1/items/840/comments" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if got := r.URL.Query().Get("expand"); got != "author" {
			t.Fatalf("expand = %q, want author", got)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Fatalf("limit = %q, want 100", got)
		}

		page := r.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		response := v1CommentsPage{
			Pagination: v1PaginationMeta{Page: 1, Limit: 100, Total: 3, TotalPages: 2},
		}
		switch page {
		case "1":
			response.Data = []v1CommentResponse{
				{ID: 3, ItemID: 840, Content: "newest", CreatedAt: time.Unix(3, 0).UTC()},
				{ID: 2, ItemID: 840, Content: "middle", CreatedAt: time.Unix(2, 0).UTC()},
			}
		case "2":
			response.Pagination.Page = 2
			response.Data = []v1CommentResponse{
				{ID: 1, ItemID: 840, Content: "oldest", CreatedAt: time.Unix(1, 0).UTC()},
			}
		default:
			t.Fatalf("unexpected page %q", page)
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL)
	client.SetBearerToken("test-token")
	comments, err := client.getComments(840)
	if err != nil {
		t.Fatalf("getComments: %v", err)
	}
	if len(comments) != 3 ||
		comments[0].ID != 3 ||
		comments[1].ID != 2 ||
		comments[2].ID != 1 {
		t.Fatalf("comments = %+v, want IDs 3, 2, 1", comments)
	}
	if len(requestedPages) != 2 ||
		requestedPages[0] != "1" ||
		requestedPages[1] != "2" {
		t.Fatalf("requested pages = %v, want [1 2]", requestedPages)
	}
}
