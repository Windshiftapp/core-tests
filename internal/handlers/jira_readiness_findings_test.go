package handlers

import (
	"context"
	"strings"
	"testing"

	"windshift/internal/jira"
)

type fakeJiraReadinessConfigurationClient struct {
	jira.Client
	issueTypes []jira.JiraIssueType
	workflow   *jira.JiraProjectWorkflowConfiguration
	screen     *jira.JiraProjectScreenConfiguration
}

func (c *fakeJiraReadinessConfigurationClient) GetProjectIssueTypes(
	context.Context,
	string,
) ([]jira.JiraIssueType, error) {
	return c.issueTypes, nil
}

func (c *fakeJiraReadinessConfigurationClient) GetProjectWorkflowConfiguration(
	context.Context,
	string,
	[]string,
) (*jira.JiraProjectWorkflowConfiguration, error) {
	return c.workflow, nil
}

func (c *fakeJiraReadinessConfigurationClient) GetProjectScreenConfiguration(
	context.Context,
	string,
	string,
	[]string,
) (*jira.JiraProjectScreenConfiguration, error) {
	return c.screen, nil
}

func TestJiraReadinessReportsExternalIssueLinksAsIntegrationLinks(t *testing.T) {
	t.Parallel()

	findings := buildFindings(&projectScanTally{
		sampled:       1,
		linkTypeUsage: map[string]int{"Relates": 2},
	}, map[string]jira.FieldMappingSuggestion{}, false)

	for _, finding := range findings {
		if finding.Entity != "Issue links: Relates" {
			continue
		}
		if !strings.Contains(finding.Reason, "integration links") ||
			strings.Contains(finding.Reason, "dropped") {
			t.Fatalf("link finding reason = %q", finding.Reason)
		}
		return
	}
	t.Fatalf("issue-link finding missing from %#v", findings)
}

func TestJiraReadinessReportsUnknownWorkflowRulesAsLossy(t *testing.T) {
	client := &fakeJiraReadinessConfigurationClient{
		issueTypes: []jira.JiraIssueType{{ID: "bug", Name: "Bug"}},
		workflow: &jira.JiraProjectWorkflowConfiguration{
			IssueTypeWorkflowIDs: map[string]string{"bug": "workflow-1"},
			Workflows: []jira.JiraWorkflowConfiguration{{
				ID:        "workflow-1",
				StatusIDs: []string{"open", "done"},
				Transitions: []jira.JiraConfiguredWorkflowTransition{
					{Type: jira.JiraWorkflowTransitionInitial, ToStatusID: "open"},
					{Type: jira.JiraWorkflowTransitionDirected, FromStatusIDs: []string{"open"}, ToStatusID: "done"},
				},
			}},
		},
		screen: &jira.JiraProjectScreenConfiguration{},
	}

	findings := (&JiraImportHandler{}).jiraConfigurationReadinessFindings(
		context.Background(),
		client,
		&jira.JiraProject{ID: "project-1", Name: "APP"},
		"APP",
		map[string]jira.FieldMappingSuggestion{},
	)

	for _, finding := range findings {
		if finding.Entity == "Workflow transition activation" {
			t.Fatalf("stale blocked workflow finding = %#v", finding)
		}
		if finding.Entity != "Workflow rule visibility" {
			continue
		}
		if finding.Severity != jira.SeverityLossy ||
			!strings.Contains(finding.Reason, "not recreated") {
			t.Fatalf("workflow rule finding = %#v, want lossy omitted-rule finding", finding)
		}
		return
	}
	t.Fatalf("workflow rule visibility finding missing from %#v", findings)
}
