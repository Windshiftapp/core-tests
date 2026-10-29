package handlers

import (
	"context"
	"testing"

	"windshift/internal/jira"
)

type fakeJiraIssueSubresourceClient struct {
	jira.Client
	commentPage *jira.JiraCommentContainer
	worklogPage *jira.JiraWorklogContainer
	watchers    *jira.JiraIssueWatchers
	jsmComments []jira.JiraServiceDeskComment
}

func (f *fakeJiraIssueSubresourceClient) GetIssueComments(
	_ context.Context,
	_ string,
	_, _ int,
) (*jira.JiraCommentContainer, error) {
	return f.commentPage, nil
}

func (f *fakeJiraIssueSubresourceClient) GetIssueWorklogs(
	_ context.Context,
	_ string,
	_, _ int,
) (*jira.JiraWorklogContainer, error) {
	return f.worklogPage, nil
}

func (f *fakeJiraIssueSubresourceClient) GetIssueWatchers(
	_ context.Context,
	_ string,
) (*jira.JiraIssueWatchers, error) {
	return f.watchers, nil
}

func (f *fakeJiraIssueSubresourceClient) ListServiceDeskRequestComments(
	_ context.Context,
	_ string,
) ([]jira.JiraServiceDeskComment, error) {
	return f.jsmComments, nil
}

func TestJiraIssueSubresourcesCompletePaginationVisibilityAndWatchers(t *testing.T) {
	handler := &JiraImportHandler{}
	public := true
	client := &fakeJiraIssueSubresourceClient{
		commentPage: &jira.JiraCommentContainer{
			Comments: []jira.JiraComment{{ID: "comment-2"}},
			Total:    2,
		},
		worklogPage: &jira.JiraWorklogContainer{
			Worklogs: []jira.JiraWorklog{{ID: "worklog-2"}},
			Total:    2,
		},
		watchers: &jira.JiraIssueWatchers{
			WatchCount: 1,
			Watchers:   []jira.JiraUser{{AccountID: "watcher-1"}},
		},
		jsmComments: []jira.JiraServiceDeskComment{{ID: "comment-1", Public: public}},
	}
	issue := &jira.JiraIssue{
		Key: "HELP-1",
		Fields: jira.JiraIssueFields{
			Comment: &jira.JiraCommentContainer{
				Comments: []jira.JiraComment{{ID: "comment-1"}},
				Total:    2,
			},
			Worklog: &jira.JiraWorklogContainer{
				Worklogs: []jira.JiraWorklog{{ID: "worklog-1"}},
				Total:    2,
			},
		},
	}

	if err := handler.completePagedIssueContainers(t.Context(), issue, client, nil); err != nil {
		t.Fatalf("complete paged containers: %v", err)
	}
	handler.completeIssueWatchers(t.Context(), issue, client)
	if err := handler.annotateJiraServiceDeskCommentVisibility(t.Context(), issue, client); err != nil {
		t.Fatalf("annotate comment visibility: %v", err)
	}

	if len(issue.Fields.Comment.Comments) != 2 ||
		issue.Fields.Comment.Comments[0].ID != "comment-1" ||
		issue.Fields.Comment.Comments[1].ID != "comment-2" {
		t.Fatalf("completed comments = %#v", issue.Fields.Comment.Comments)
	}
	if len(issue.Fields.Worklog.Worklogs) != 2 ||
		issue.Fields.Worklog.Worklogs[1].ID != "worklog-2" {
		t.Fatalf("completed worklogs = %#v", issue.Fields.Worklog.Worklogs)
	}
	if issue.Fields.Comment.Comments[0].ServiceDeskPublic == nil ||
		!*issue.Fields.Comment.Comments[0].ServiceDeskPublic ||
		issue.Fields.Comment.Comments[1].ServiceDeskPublic == nil ||
		*issue.Fields.Comment.Comments[1].ServiceDeskPublic {
		t.Fatalf("comment visibility = %#v", issue.Fields.Comment.Comments)
	}
	if !issue.Fields.WatcherIdentitiesAvailable ||
		len(issue.Fields.Watchers) != 1 ||
		issue.Fields.Watchers[0].AccountID != "watcher-1" {
		t.Fatalf("watchers = %#v", issue.Fields)
	}
}
