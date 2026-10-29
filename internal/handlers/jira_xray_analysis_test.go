package handlers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"windshift/internal/jira"
)

type fakeXrayAnalysisClient struct {
	jira.Client
	detectedIDs   []string
	keysByProject map[string][]string
	detectErr     error
	listErr       error
}

func (f *fakeXrayAnalysisClient) DetectXrayTestIssueTypes(_ context.Context, _ []jira.JiraIssueType) ([]string, error) {
	return f.detectedIDs, f.detectErr
}

func (f *fakeXrayAnalysisClient) ListXrayTestKeys(_ context.Context, projectKey string, issueTypeIDs []string, _ bool) ([]string, error) {
	if !reflect.DeepEqual(issueTypeIDs, f.detectedIDs) {
		return nil, errors.New("unverified issue type IDs passed to Xray listing")
	}
	return f.keysByProject[projectKey], f.listErr
}

func TestAnalyzeXrayTestsOnlyCountsPositivelyDetectedTypes(t *testing.T) {
	t.Parallel()

	client := &fakeXrayAnalysisClient{
		detectedIDs: []string{"10001"},
		keysByProject: map[string][]string{
			"APP": {"APP-2", "APP-9"},
			"OPS": {},
		},
	}
	got := analyzeXrayTests(context.Background(), client, map[string][]jira.JiraIssueType{
		"APP": {
			{ID: "10001", Name: "Renamed acceptance scenario"},
			{ID: "10002", Name: "Test Case"},
		},
		"OPS": {
			{ID: "10002", Name: "Test"},
		},
	}, false)

	if got.DetectionStatus != "detected" || got.TotalTests != 2 {
		t.Fatalf("analysis = %#v", got)
	}
	if want := []string{"10001"}; !reflect.DeepEqual(got.TestIssueTypeIDs, want) {
		t.Fatalf("TestIssueTypeIDs = %#v, want %#v", got.TestIssueTypeIDs, want)
	}
	if len(got.Projects) != 1 || got.Projects[0].ProjectKey != "APP" || got.Projects[0].TestCount != 2 {
		t.Fatalf("Projects = %#v", got.Projects)
	}
}

func TestAnalyzeXrayTestsDoesNotGuessWhenPropertyLookupFails(t *testing.T) {
	t.Parallel()

	client := &fakeXrayAnalysisClient{detectErr: errors.New("forbidden")}
	got := analyzeXrayTests(context.Background(), client, map[string][]jira.JiraIssueType{
		"APP": {{ID: "10001", Name: "Test Case"}},
	}, false)

	if got.DetectionStatus != "unavailable" || got.TotalTests != 0 {
		t.Fatalf("analysis = %#v", got)
	}
}
