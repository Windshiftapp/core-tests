package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"windshift/internal/jira"
)

type captureCapabilityClient struct {
	jira.Client
	workflowResponse *jira.JiraProjectWorkflowConfiguration
	screenResponse   *jira.JiraProjectScreenConfiguration
	workflowProject  string
	workflowTypes    []string
	screenProject    string
	screenKey        string
	screenTypes      []string
}

func (c *captureCapabilityClient) GetProjectWorkflowConfiguration(
	_ context.Context,
	projectID string,
	issueTypeIDs []string,
) (*jira.JiraProjectWorkflowConfiguration, error) {
	c.workflowProject = projectID
	c.workflowTypes = append([]string(nil), issueTypeIDs...)
	return c.workflowResponse, nil
}

func (c *captureCapabilityClient) GetProjectScreenConfiguration(
	_ context.Context,
	projectID string,
	projectKey string,
	issueTypeIDs []string,
) (*jira.JiraProjectScreenConfiguration, error) {
	c.screenProject = projectID
	c.screenKey = projectKey
	c.screenTypes = append([]string(nil), issueTypeIDs...)
	return c.screenResponse, nil
}

func TestRecordingClientCapturesWorkflowConfiguration(t *testing.T) {
	captureDir := t.TempDir()
	path := filepath.Join(captureDir, "jira_project_workflow_configuration.jsonl")
	if err := os.WriteFile(path, []byte("stale capture\n"), 0o600); err != nil {
		t.Fatalf("seed stale workflow capture: %v", err)
	}

	response := &jira.JiraProjectWorkflowConfiguration{
		IssueTypeWorkflowIDs: map[string]string{"story": "workflow-1"},
		Workflows:            []jira.JiraWorkflowConfiguration{{ID: "workflow-1", Name: "Software workflow"}},
		RulesComplete:        true,
	}
	client := &captureCapabilityClient{workflowResponse: response}
	recorder := newRecordingClient(client, captureDir)

	got, err := recorder.GetProjectWorkflowConfiguration(context.Background(), "project-1", []string{"story", "bug"})
	if err != nil {
		t.Fatalf("GetProjectWorkflowConfiguration: %v", err)
	}
	if got != response {
		t.Fatalf("response pointer = %p, want %p", got, response)
	}
	if client.workflowProject != "project-1" || !reflect.DeepEqual(client.workflowTypes, []string{"story", "bug"}) {
		t.Fatalf("workflow inputs = %q/%v, want project-1/[story bug]", client.workflowProject, client.workflowTypes)
	}

	assertSingleJiraCaptureRecord(t, path, map[string]any{
		"project_id":     "project-1",
		"issue_type_ids": []any{"story", "bug"},
		"response":       response,
	})
}

func TestRecordingClientCapturesScreenConfiguration(t *testing.T) {
	captureDir := t.TempDir()
	path := filepath.Join(captureDir, "jira_project_screen_configuration.jsonl")
	if err := os.WriteFile(path, []byte("stale capture\n"), 0o600); err != nil {
		t.Fatalf("seed stale screen capture: %v", err)
	}

	response := &jira.JiraProjectScreenConfiguration{
		IssueTypeScreens: map[string]jira.JiraIssueTypeScreens{
			"story": {CreateScreenID: "screen-create", EditScreenID: "screen-edit", ViewScreenID: "screen-view"},
		},
		Screens: []jira.JiraScreenConfiguration{{ID: "screen-create", Name: "Create"}},
	}
	client := &captureCapabilityClient{screenResponse: response}
	recorder := newRecordingClient(client, captureDir)

	got, err := recorder.GetProjectScreenConfiguration(context.Background(), "project-1", "APP", []string{"story"})
	if err != nil {
		t.Fatalf("GetProjectScreenConfiguration: %v", err)
	}
	if got != response {
		t.Fatalf("response pointer = %p, want %p", got, response)
	}
	if client.screenProject != "project-1" || client.screenKey != "APP" || !reflect.DeepEqual(client.screenTypes, []string{"story"}) {
		t.Fatalf("screen inputs = %q/%q/%v, want project-1/APP/[story]", client.screenProject, client.screenKey, client.screenTypes)
	}

	assertSingleJiraCaptureRecord(t, path, map[string]any{
		"project_id":     "project-1",
		"project_key":    "APP",
		"issue_type_ids": []any{"story"},
		"response":       response,
	})
}

func assertSingleJiraCaptureRecord(t *testing.T, path string, expected map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	var records []json.RawMessage
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			records = append(records, json.RawMessage(line))
		}
	}
	if len(records) != 1 {
		t.Fatalf("capture %s contains %d records, want exactly one", path, len(records))
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(records[0], &got); err != nil {
		t.Fatalf("decode capture %s: %v", path, err)
	}
	if len(got) != len(expected) {
		t.Fatalf("capture keys = %v, want %d keys", reflect.ValueOf(got).MapKeys(), len(expected))
	}
	for key, want := range expected {
		gotRaw, ok := got[key]
		if !ok {
			t.Fatalf("capture missing key %q", key)
		}
		wantRaw, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshal expected %s: %v", key, err)
		}
		var gotValue, wantValue any
		if err := json.Unmarshal(gotRaw, &gotValue); err != nil {
			t.Fatalf("decode captured %s: %v", key, err)
		}
		if err := json.Unmarshal(wantRaw, &wantValue); err != nil {
			t.Fatalf("decode expected %s: %v", key, err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("capture %s = %v, want %v", key, gotValue, wantValue)
		}
	}
}
