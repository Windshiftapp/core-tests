//go:build test

package handlers

import (
	"reflect"
	"testing"

	"windshift/internal/jira"
)

func wikiAttachmentIssue(description string, comments []jira.JiraComment, attachments ...jira.JiraAttachment) *jira.JiraIssue {
	return &jira.JiraIssue{
		ID: "10110",
		Fields: jira.JiraIssueFields{
			Description: description,
			Attachment:  attachments,
			Comment:     &jira.JiraCommentContainer{Comments: comments},
		},
	}
}

// TestJiraInternalAttachmentIDsUsesUnambiguousWikiFilenames is the WI-2062
// Data Center regression: wiki bodies name files instead of carrying ADF media
// ids, so an attachment referenced from a customer-visible body must stay
// visible.
func TestJiraInternalAttachmentIDsUsesUnambiguousWikiFilenames(t *testing.T) {
	t.Parallel()

	issue := wikiAttachmentIssue(
		"See !public.png! and [^guide.pdf] for details.",
		nil,
		jira.JiraAttachment{ID: "1", Filename: "public.png"},
		jira.JiraAttachment{ID: "2", Filename: "guide.pdf"},
		jira.JiraAttachment{ID: "3", Filename: "internal-only.txt"},
	)

	internal := jiraInternalAttachmentIDs(issue, issue.Fields.Description, true)
	want := map[string]bool{"3": true}
	if !reflect.DeepEqual(internal, want) {
		t.Fatalf("internal attachments = %#v, want %#v", internal, want)
	}
}

// TestJiraInternalAttachmentIDsOnlyCountsPublicComments proves an internal
// agent note does not make its attachment customer-visible.
func TestJiraInternalAttachmentIDsOnlyCountsPublicComments(t *testing.T) {
	t.Parallel()

	internalNote := false
	publicNote := true
	issue := wikiAttachmentIssue(
		"",
		[]jira.JiraComment{
			{Body: "internal note !hidden.png!", ServiceDeskPublic: &internalNote},
			{Body: "public note !shown.png!", ServiceDeskPublic: &publicNote},
		},
		jira.JiraAttachment{ID: "1", Filename: "hidden.png"},
		jira.JiraAttachment{ID: "2", Filename: "shown.png"},
	)

	internal := jiraInternalAttachmentIDs(issue, issue.Fields.Description, true)
	want := map[string]bool{"1": true}
	if !reflect.DeepEqual(internal, want) {
		t.Fatalf("internal attachments = %#v, want %#v", internal, want)
	}
}

// TestJiraInternalAttachmentIDsStaysFailClosedForAmbiguousNames proves a
// filename shared by several attachments cannot make any of them visible.
func TestJiraInternalAttachmentIDsStaysFailClosedForAmbiguousNames(t *testing.T) {
	t.Parallel()

	issue := wikiAttachmentIssue(
		"Attached !report.pdf!.",
		nil,
		jira.JiraAttachment{ID: "1", Filename: "report.pdf"},
		jira.JiraAttachment{ID: "2", Filename: "report.pdf"},
	)

	internal := jiraInternalAttachmentIDs(issue, issue.Fields.Description, true)
	want := map[string]bool{"1": true, "2": true}
	if !reflect.DeepEqual(internal, want) {
		t.Fatalf("internal attachments = %#v, want %#v", internal, want)
	}
}

// TestJiraInternalAttachmentIDsIgnoresNonServiceDeskImports keeps ordinary
// projects free of portal visibility restrictions.
func TestJiraInternalAttachmentIDsIgnoresNonServiceDeskImports(t *testing.T) {
	t.Parallel()

	issue := wikiAttachmentIssue("", nil, jira.JiraAttachment{ID: "1", Filename: "a.txt"})
	if got := jiraInternalAttachmentIDs(issue, issue.Fields.Description, false); got != nil {
		t.Fatalf("internal attachments = %#v, want nil for a non-service-desk import", got)
	}
}

// TestJiraUnresolvedMediaIDsReportsMediaServicesIdentifiers is the WI-2062
// Cloud regression: ADF media ids are Media Services UUIDs, so they cannot
// resolve to a numeric attachment id and must be reported instead of guessed.
func TestJiraUnresolvedMediaIDsReportsMediaServicesIdentifiers(t *testing.T) {
	t.Parallel()

	mediaID := "b3c1a2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	description := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{"type": "mediaSingle", "content": []any{
				map[string]any{"type": "media", "attrs": map[string]any{"id": mediaID}},
			}},
		},
	}
	issue := &jira.JiraIssue{ID: "10110", Fields: jira.JiraIssueFields{Description: description}}

	got := jiraUnresolvedMediaIDs(issue, description, map[string]jira.MediaAttachment{"9001": {}})
	if !reflect.DeepEqual(got, []string{mediaID}) {
		t.Fatalf("unresolved media = %#v, want [%s]", got, mediaID)
	}
}

func TestWikiReferencedFilenames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want []string
	}{
		{name: "inline image", body: "See !chart.png! here.", want: []string{"chart.png"}},
		{name: "inline image with params", body: "!chart.png|width=200!", want: []string{"chart.png"}},
		{name: "attachment link", body: "Read [^guide.pdf].", want: []string{"guide.pdf"}},
		{name: "deduplicated", body: "!a.png! and !a.png!", want: []string{"a.png"}},
		{name: "plain text", body: "no attachments here", want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := jira.WikiReferencedFilenames(test.body)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("WikiReferencedFilenames(%q) = %#v, want %#v", test.body, got, test.want)
			}
		})
	}
}
