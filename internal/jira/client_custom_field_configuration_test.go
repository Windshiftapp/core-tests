package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestCloudClientGetCustomFieldConfigurationPreservesContextsOptionsAndDefaults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/field/customfield_100/context":
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"values": [
					{"id":"10","name":"Global","isGlobalContext":true,"isAnyIssueType":true},
					{"id":"20","name":"Bugs","description":"Bug-only values","isGlobalContext":false,"isAnyIssueType":false}
				]
			}`))
		case "/rest/api/3/field/customfield_100/context/projectmapping":
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"values": [
					{"contextId":"10","isGlobalContext":true},
					{"contextId":"20","projectId":"200"}
				]
			}`))
		case "/rest/api/3/field/customfield_100/context/issuetypemapping":
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"values": [
					{"contextId":"10","isAnyIssueType":true},
					{"contextId":"20","issueTypeId":"bug"}
				]
			}`))
		case "/rest/api/3/field/customfield_100/context/defaultValues":
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"values": [
					{"contextId":20,"defaultValues":[
						{"issueTypeId":"bug","isAnyIssueType":false,"value":{"optionId":"3","type":"option.single"}}
					]}
				]
			}`))
		case "/rest/api/3/field/customfield_100/context/10/option":
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"values": [{"id":"1","value":"Global option"}]
			}`))
		case "/rest/api/3/field/customfield_100/context/20/option":
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"values": [
					{"id":"2","value":"Parent"},
					{"id":"3","value":"Child","optionId":"2","disabled":true}
				]
			}`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
	config, err := client.GetCustomFieldConfiguration(context.Background(), "customfield_100", true)
	if err != nil {
		t.Fatalf("GetCustomFieldConfiguration() error = %v", err)
	}
	if config.FieldID != "customfield_100" || len(config.Contexts) != 2 {
		t.Fatalf("configuration = %#v", config)
	}
	global := config.Contexts[0]
	if global.ID != "10" || !global.IsGlobal || !global.IsAnyIssueType ||
		len(global.Options) != 1 || global.Options[0].Value != "Global option" {
		t.Fatalf("global context = %#v", global)
	}
	bugs := config.Contexts[1]
	if bugs.ID != "20" || len(bugs.ProjectIDs) != 1 || bugs.ProjectIDs[0] != "200" ||
		len(bugs.IssueTypeIDs) != 1 || bugs.IssueTypeIDs[0] != "bug" {
		t.Fatalf("bug context applicability = %#v", bugs)
	}
	if len(bugs.Options) != 2 || bugs.Options[1].ParentOptionID != "2" || !bugs.Options[1].Disabled {
		t.Fatalf("bug context options = %#v", bugs.Options)
	}
	if len(bugs.Defaults) != 1 || bugs.Defaults[0].IssueTypeID != "bug" {
		t.Fatalf("bug context defaults = %#v", bugs.Defaults)
	}
}

func TestCloudClientCustomFieldConfigurationRetainsContextWhenOptionsUnsupported(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/field/customfield_101/context":
			_, _ = w.Write([]byte(`{"isLast":true,"values":[{"id":"10","name":"Groups","isGlobalContext":true}]}`))
		case "/rest/api/3/field/customfield_101/context/projectmapping",
			"/rest/api/3/field/customfield_101/context/issuetypemapping",
			"/rest/api/3/field/customfield_101/context/defaultValues":
			_, _ = w.Write([]byte(`{"isLast":true,"values":[]}`))
		case "/rest/api/3/field/customfield_101/context/10/option":
			http.Error(w, `{"errorMessages":["The custom field doesn't support options."]}`, http.StatusBadRequest)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
	config, err := client.GetCustomFieldConfiguration(context.Background(), "customfield_101", true)
	if err != nil {
		t.Fatalf("GetCustomFieldConfiguration() error = %v", err)
	}
	if len(config.Contexts) != 1 ||
		config.Contexts[0].Name != "Groups" ||
		config.Contexts[0].OptionsUnavailableReason == "" {
		t.Fatalf("configuration = %#v", config)
	}
}
