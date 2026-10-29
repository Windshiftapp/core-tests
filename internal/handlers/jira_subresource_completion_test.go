package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"windshift/internal/jira"
)

type fakeJiraSubresourceClient struct {
	jira.Client
	getComments func(startAt int) (*jira.JiraCommentContainer, error)
	getWorklogs func(startAt int) (*jira.JiraWorklogContainer, error)
}

func (f *fakeJiraSubresourceClient) GetIssueComments(_ context.Context, _ string, startAt, _ int) (*jira.JiraCommentContainer, error) {
	return f.getComments(startAt)
}

func (f *fakeJiraSubresourceClient) GetIssueWorklogs(_ context.Context, _ string, startAt, _ int) (*jira.JiraWorklogContainer, error) {
	return f.getWorklogs(startAt)
}

func comment(id string) jira.JiraComment {
	return jira.JiraComment{ID: id, Body: "comment " + id}
}

func worklog(id string) jira.JiraWorklog {
	return jira.JiraWorklog{ID: id, TimeSpentSeconds: 60}
}

// TestCompleteIssueCommentsPreservesFetchedPagesAndReportsIncomplete is the
// WI-1706 contract for the comment fetch: successful pagination returns every
// page, while a later-page failure or a premature empty page keeps the pages
// already fetched and returns an error so the caller can report the loss.
func TestCompleteIssueCommentsPreservesFetchedPagesAndReportsIncomplete(t *testing.T) {
	t.Parallel()

	newIssue := func() *jira.JiraIssue {
		return &jira.JiraIssue{
			Key: "APP-1",
			Fields: jira.JiraIssueFields{
				Comment: &jira.JiraCommentContainer{
					Comments:   []jira.JiraComment{comment("c1")},
					Total:      3,
					MaxResults: 1,
				},
			},
		}
	}

	tests := []struct {
		name         string
		pages        func(startAt int) (*jira.JiraCommentContainer, error)
		wantErr      bool
		wantComments []string
	}{
		{
			name: "successful pagination keeps every page",
			pages: func(startAt int) (*jira.JiraCommentContainer, error) {
				return &jira.JiraCommentContainer{Comments: []jira.JiraComment{comment("c" + string(rune('0'+startAt+1)))}, Total: 3}, nil
			},
			wantComments: []string{"c1", "c2", "c3"},
		},
		{
			name: "later-page failure preserves earlier pages",
			pages: func(startAt int) (*jira.JiraCommentContainer, error) {
				if startAt == 1 {
					return &jira.JiraCommentContainer{Comments: []jira.JiraComment{comment("c2")}, Total: 3}, nil
				}
				return nil, errors.New("comments page unavailable")
			},
			wantErr:      true,
			wantComments: []string{"c1", "c2"},
		},
		{
			name: "premature empty page preserves earlier pages",
			pages: func(startAt int) (*jira.JiraCommentContainer, error) {
				if startAt == 1 {
					return &jira.JiraCommentContainer{Comments: []jira.JiraComment{comment("c2")}, Total: 3}, nil
				}
				return &jira.JiraCommentContainer{}, nil
			},
			wantErr:      true,
			wantComments: []string{"c1", "c2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			issue := newIssue()
			client := &fakeJiraSubresourceClient{getComments: tt.pages}
			handler := &JiraImportHandler{}
			err := handler.completeIssueComments(t.Context(), issue, client)
			if (err != nil) != tt.wantErr {
				t.Fatalf("completeIssueComments() error = %v, wantErr %v", err, tt.wantErr)
			}
			var got []string
			for _, c := range issue.Fields.Comment.Comments {
				got = append(got, c.ID)
			}
			if strings.Join(got, ",") != strings.Join(tt.wantComments, ",") {
				t.Fatalf("comments = %v, want %v", got, tt.wantComments)
			}
		})
	}
}

func TestCompleteIssueWorklogsPreservesFetchedPagesAndReportsIncomplete(t *testing.T) {
	t.Parallel()

	newIssue := func() *jira.JiraIssue {
		return &jira.JiraIssue{
			Key: "APP-1",
			Fields: jira.JiraIssueFields{
				Worklog: &jira.JiraWorklogContainer{
					Worklogs:   []jira.JiraWorklog{worklog("w1")},
					Total:      2,
					MaxResults: 1,
				},
			},
		}
	}

	t.Run("later-page failure preserves earlier pages", func(t *testing.T) {
		t.Parallel()
		issue := newIssue()
		client := &fakeJiraSubresourceClient{getWorklogs: func(int) (*jira.JiraWorklogContainer, error) {
			return nil, errors.New("worklogs page unavailable")
		}}
		handler := &JiraImportHandler{}
		if err := handler.completeIssueWorklogs(t.Context(), issue, client); err == nil {
			t.Fatal("completeIssueWorklogs() error = nil, want later-page failure")
		}
		if len(issue.Fields.Worklog.Worklogs) != 1 || issue.Fields.Worklog.Worklogs[0].ID != "w1" {
			t.Fatalf("worklogs = %#v, want the embedded page preserved", issue.Fields.Worklog.Worklogs)
		}
	})

	t.Run("successful pagination keeps every page", func(t *testing.T) {
		t.Parallel()
		issue := newIssue()
		client := &fakeJiraSubresourceClient{getWorklogs: func(int) (*jira.JiraWorklogContainer, error) {
			return &jira.JiraWorklogContainer{Worklogs: []jira.JiraWorklog{worklog("w2")}, Total: 2}, nil
		}}
		handler := &JiraImportHandler{}
		if err := handler.completeIssueWorklogs(t.Context(), issue, client); err != nil {
			t.Fatalf("completeIssueWorklogs() error = %v", err)
		}
		if len(issue.Fields.Worklog.Worklogs) != 2 {
			t.Fatalf("worklogs = %#v, want 2", issue.Fields.Worklog.Worklogs)
		}
	})
}

func TestCompletePagedIssueContainersCountsFetchFailures(t *testing.T) {
	t.Parallel()

	issue := &jira.JiraIssue{
		Key: "APP-1",
		Fields: jira.JiraIssueFields{
			Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{comment("c1")}, Total: 2, MaxResults: 1},
			Worklog: &jira.JiraWorklogContainer{Worklogs: []jira.JiraWorklog{worklog("w1")}, Total: 2, MaxResults: 1},
		},
	}
	client := &fakeJiraSubresourceClient{
		getComments: func(int) (*jira.JiraCommentContainer, error) { return nil, errors.New("down") },
		getWorklogs: func(int) (*jira.JiraWorklogContainer, error) { return nil, errors.New("down") },
	}
	progress := &ImportProgress{}
	handler := &JiraImportHandler{}
	_ = handler.completePagedIssueContainers(t.Context(), issue, client, progress)

	if progress.FailedComments != 1 {
		t.Errorf("FailedComments = %d, want 1", progress.FailedComments)
	}
	if progress.FailedWorklogs != 1 {
		t.Errorf("FailedWorklogs = %d, want 1", progress.FailedWorklogs)
	}
}

// TestJiraImportTerminalOutcomeIncludesSubresourceFailures asserts comment and
// worklog losses change the terminal status instead of reporting success.
func TestJiraImportTerminalOutcomeIncludesSubresourceFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		progress   ImportProgress
		wantStatus string
	}{
		{
			name:       "comment loss with imported issues is completed with errors",
			progress:   ImportProgress{ImportedIssues: 3, FailedComments: 2},
			wantStatus: "completed_with_errors",
		},
		{
			name:       "worklog loss with imported issues is completed with errors",
			progress:   ImportProgress{ImportedIssues: 1, FailedWorklogs: 1},
			wantStatus: "completed_with_errors",
		},
		{
			name:       "only comment loss fails the job",
			progress:   ImportProgress{FailedComments: 1},
			wantStatus: "failed",
		},
		{
			name:       "no failures completes",
			progress:   ImportProgress{ImportedIssues: 1},
			wantStatus: "completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, _, _ := jiraImportTerminalOutcome(&tt.progress)
			if status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}
