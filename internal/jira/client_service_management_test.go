package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestCloudClientReadsServiceManagementEntitiesWithGet(t *testing.T) {
	t.Parallel()

	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/servicedeskapi/servicedesk":
			_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraServiceDesk]{
				IsLastPage: true,
				Values:     []JiraServiceDesk{{ID: "1", ProjectKey: "HELP"}},
			})
		case "/rest/servicedeskapi/servicedesk/1/requesttype":
			_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraServiceDeskRequestType]{
				IsLastPage: true,
				Values:     []JiraServiceDeskRequestType{{ID: "2", Name: "Get help"}},
			})
		case "/rest/servicedeskapi/request/HELP-1/comment":
			_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraServiceDeskComment]{
				IsLastPage: true,
				Values:     []JiraServiceDeskComment{{ID: "3", Public: false}},
			})
		case "/rest/servicedeskapi/servicedesk/1/organization":
			_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraServiceDeskOrganization]{
				IsLastPage: true,
				Values:     []JiraServiceDeskOrganization{{ID: "4", Name: "Example Customer"}},
			})
		case "/rest/servicedeskapi/organization/4/user":
			_ = json.NewEncoder(w).Encode(JiraServiceDeskPage[JiraUser]{
				IsLastPage: true,
				Values:     []JiraUser{{AccountID: "qm:customer", DisplayName: "Customer"}},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &cloudClient{
		serviceDeskURL: server.URL + "/rest/servicedeskapi",
		httpClient:     server.Client(),
		limiter:        rate.NewLimiter(rate.Inf, 1),
	}

	desks, err := client.ListServiceDesks(context.Background())
	if err != nil || len(desks) != 1 || desks[0].ProjectKey != "HELP" {
		t.Fatalf("ListServiceDesks() = %#v, %v", desks, err)
	}
	requestTypes, err := client.ListServiceDeskRequestTypes(context.Background(), "1")
	if err != nil || len(requestTypes) != 1 || requestTypes[0].ID != "2" {
		t.Fatalf("ListServiceDeskRequestTypes() = %#v, %v", requestTypes, err)
	}
	comments, err := client.ListServiceDeskRequestComments(context.Background(), "HELP-1")
	if err != nil || len(comments) != 1 || comments[0].Public {
		t.Fatalf("ListServiceDeskRequestComments() = %#v, %v", comments, err)
	}
	organizations, err := client.ListServiceDeskOrganizations(context.Background(), "1")
	if err != nil || len(organizations) != 1 || organizations[0].ID != "4" {
		t.Fatalf("ListServiceDeskOrganizations() = %#v, %v", organizations, err)
	}
	users, err := client.ListServiceDeskOrganizationUsers(context.Background(), "4")
	if err != nil || len(users) != 1 || users[0].GetIdentifier() != "qm:customer" {
		t.Fatalf("ListServiceDeskOrganizationUsers() = %#v, %v", users, err)
	}
	if len(methods) != 5 {
		t.Fatalf("request count = %d, want 5", len(methods))
	}
	for idx, method := range methods {
		if method != http.MethodGet {
			t.Fatalf("request %d method = %s, want GET", idx, method)
		}
	}
}
