//go:build test

package data

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetTimeProjectsUsesBearerV1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/v2/time/projects" {
			t.Errorf("request = %s %s, want GET /rest/api/v2/time/projects", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("status"); got != "Active" {
			t.Errorf("status filter = %q, want Active", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tui-token" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("X-Session-Token"); got != "" {
			t.Errorf("X-Session-Token = %q, want empty", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"id": 17, "name": "Project\x1b[31m", "description": "Description\x1b[31m", "customer_name": "Customer\x1b[31m",
		}}})
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL)
	client.SetBearerToken("tui-token")
	projects, err := client.getTimeProjects()
	if err != nil {
		t.Fatalf("getTimeProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("projects = %+v, want one project", projects)
	}
	project := projects[0]
	if project.ID != 17 || project.Name != "Project" {
		t.Errorf("project = %+v, want sanitized v1 project", project)
	}
	if project.Description == nil || *project.Description != "Description" {
		t.Errorf("description = %v, want sanitized value", project.Description)
	}
	if project.CustomerName == nil || *project.CustomerName != "Customer" {
		t.Errorf("customer_name = %v, want sanitized value", project.CustomerName)
	}
}

func TestClientCreateTimeLogUsesBearerV1Contract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/v2/time/worklogs" {
			t.Errorf("request = %s %s, want POST /rest/api/v2/time/worklogs", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tui-token" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("X-Session-Token"); got != "" {
			t.Errorf("X-Session-Token = %q, want empty", got)
		}
		var body struct {
			ProjectID   int    `json:"project_id"`
			ItemID      int    `json:"item_id"`
			Description string `json:"description"`
			Date        string `json:"date"`
			StartTime   string `json:"start_time"`
			Duration    string `json:"duration"`
			Timezone    string `json:"timezone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		want := struct {
			ProjectID   int    `json:"project_id"`
			ItemID      int    `json:"item_id"`
			Description string `json:"description"`
			Date        string `json:"date"`
			StartTime   string `json:"start_time"`
			Duration    string `json:"duration"`
			Timezone    string `json:"timezone"`
		}{23, 41, "Implemented convergence", "2026-09-02", "09:15", "1h30m", "Europe/Zurich"}
		if body != want {
			t.Errorf("body = %+v, want %+v", body, want)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":91}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL)
	client.SetBearerToken("tui-token")
	if err := client.createTimeLog(41, 23, "Implemented convergence", "1h30m", "2026-09-02", "09:15", "Europe/Zurich"); err != nil {
		t.Fatalf("createTimeLog: %v", err)
	}
}
