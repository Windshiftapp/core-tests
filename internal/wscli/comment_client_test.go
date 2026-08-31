package wscli

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_GetCommentsFollowsAllPages(t *testing.T) {
	requestedPages := make([]string, 0, 2)
	client, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/v1/items/840/comments" {
			http.NotFound(w, r)
			return
		}
		requestedPages = append(requestedPages, r.URL.RawQuery)
		page := r.URL.Query().Get("page")
		response := PaginatedResponse[Comment]{
			Pagination: PaginationMeta{Page: 1, Limit: 100, Total: 3, TotalPages: 2},
		}
		switch page {
		case "1":
			response.Data = []Comment{{ID: 3}, {ID: 2}}
		case "2":
			response.Pagination.Page = 2
			response.Data = []Comment{{ID: 1}}
		default:
			t.Fatalf("unexpected page %q", page)
		}
		_ = json.NewEncoder(w).Encode(response)
	})

	comments, err := client.GetComments(840)
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 3 ||
		comments[0].ID != 3 ||
		comments[1].ID != 2 ||
		comments[2].ID != 1 {
		t.Fatalf("comments = %+v, want IDs 3, 2, 1", comments)
	}
	if len(requestedPages) != 2 ||
		requestedPages[0] != "page=1&limit=100" ||
		requestedPages[1] != "page=2&limit=100" {
		t.Fatalf("requested pages = %v", requestedPages)
	}
}
