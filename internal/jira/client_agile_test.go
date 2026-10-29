package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestCloudClientListBoardsPaginates(t *testing.T) {
	var seenStartAt []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("projectKeyOrId"); got != "PROJ" {
			t.Fatalf("projectKeyOrId = %q, want PROJ", got)
		}
		seenStartAt = append(seenStartAt, r.URL.Query().Get("startAt"))

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("startAt") {
		case "0":
			_ = json.NewEncoder(w).Encode(BoardListResult{
				MaxResults: 1,
				StartAt:    0,
				Total:      2,
				IsLast:     false,
				Values:     []JiraBoard{{ID: 10, Name: "A"}},
			})
		case "1":
			_ = json.NewEncoder(w).Encode(BoardListResult{
				MaxResults: 1,
				StartAt:    1,
				Total:      2,
				IsLast:     true,
				Values:     []JiraBoard{{ID: 11, Name: "B"}},
			})
		default:
			t.Fatalf("unexpected startAt: %s", r.URL.Query().Get("startAt"))
		}
	}))
	defer server.Close()

	client := &cloudClient{
		agileURL:   server.URL + "/rest/agile/1.0",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	result, err := client.ListBoards(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("ListBoards returned error: %v", err)
	}
	if fmt.Sprint(seenStartAt) != "[0 1]" {
		t.Fatalf("startAt sequence = %v, want [0 1]", seenStartAt)
	}
	if len(result.Values) != 2 || result.Values[0].ID != 10 || result.Values[1].ID != 11 {
		t.Fatalf("unexpected boards: %#v", result.Values)
	}
}

func TestCloudClientGetBoardSprintsPaginates(t *testing.T) {
	var seenStartAt []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board/42/sprint" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seenStartAt = append(seenStartAt, r.URL.Query().Get("startAt"))

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("startAt") {
		case "0":
			_ = json.NewEncoder(w).Encode(SprintListResult{
				MaxResults: 1,
				StartAt:    0,
				Total:      2,
				IsLast:     false,
				Values:     []JiraSprint{{ID: 100, Name: "Sprint 1"}},
			})
		case "1":
			_ = json.NewEncoder(w).Encode(SprintListResult{
				MaxResults: 1,
				StartAt:    1,
				Total:      2,
				IsLast:     true,
				Values:     []JiraSprint{{ID: 101, Name: "Sprint 2"}},
			})
		default:
			t.Fatalf("unexpected startAt: %s", r.URL.Query().Get("startAt"))
		}
	}))
	defer server.Close()

	client := &cloudClient{
		agileURL:   server.URL + "/rest/agile/1.0",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	result, err := client.GetBoardSprints(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetBoardSprints returned error: %v", err)
	}
	if fmt.Sprint(seenStartAt) != "[0 1]" {
		t.Fatalf("startAt sequence = %v, want [0 1]", seenStartAt)
	}
	if len(result.Values) != 2 || result.Values[0].ID != 100 || result.Values[1].ID != 101 {
		t.Fatalf("unexpected sprints: %#v", result.Values)
	}
}
