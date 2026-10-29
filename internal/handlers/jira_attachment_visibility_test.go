package handlers

import (
	"testing"

	"windshift/internal/jira"
)

func adfDocWithMedia(ids ...string) map[string]any {
	content := make([]any, 0, len(ids))
	for _, id := range ids {
		content = append(content, map[string]any{
			"type":  "media",
			"attrs": map[string]any{"id": id},
		})
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

func jiraAttachmentIDs(ids ...string) []jira.JiraAttachment {
	attachments := make([]jira.JiraAttachment, 0, len(ids))
	for _, id := range ids {
		attachments = append(attachments, jira.JiraAttachment{ID: id, Filename: id + ".png"})
	}
	return attachments
}

// TestJiraInternalAttachmentIDs pins WI-1700: an attachment is hidden from
// portal customers unless it is referenced from a body the customer can see.
// Internal-note attachments and unreferenced files stay internal.
func TestJiraInternalAttachmentIDs(t *testing.T) {
	t.Parallel()

	public := true
	internal := false

	tests := []struct {
		name             string
		issue            *jira.JiraIssue
		description      any
		serviceDesk      bool
		wantInternal     []string
		wantNotInternal  []string
	}{
		{
			name: "public description and public comment stay visible",
			issue: &jira.JiraIssue{Fields: jira.JiraIssueFields{
				Attachment: jiraAttachmentIDs("desc-file", "public-file"),
				Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{
					{ID: "c1", Body: adfDocWithMedia("public-file"), ServiceDeskPublic: &public},
				}},
			}},
			description:  adfDocWithMedia("desc-file"),
			serviceDesk:  true,
			wantNotInternal: []string{"desc-file", "public-file"},
		},
		{
			name: "internal comment attachment is hidden",
			issue: &jira.JiraIssue{Fields: jira.JiraIssueFields{
				Attachment: jiraAttachmentIDs("internal-file"),
				Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{
					{ID: "c1", Body: adfDocWithMedia("internal-file"), ServiceDeskPublic: &internal},
				}},
			}},
			description:  nil,
			serviceDesk:  true,
			wantInternal: []string{"internal-file"},
		},
		{
			name: "unreferenced attachment is hidden",
			issue: &jira.JiraIssue{Fields: jira.JiraIssueFields{
				Attachment: jiraAttachmentIDs("orphan-file"),
				Comment:    &jira.JiraCommentContainer{Comments: []jira.JiraComment{}},
			}},
			description:  adfDocWithMedia("desc-file"),
			serviceDesk:  true,
			wantInternal: []string{"orphan-file"},
		},
		{
			name: "attachment shared by public and internal comment stays visible",
			issue: &jira.JiraIssue{Fields: jira.JiraIssueFields{
				Attachment: jiraAttachmentIDs("shared-file"),
				Comment: &jira.JiraCommentContainer{Comments: []jira.JiraComment{
					{ID: "c1", Body: adfDocWithMedia("shared-file"), ServiceDeskPublic: &internal},
					{ID: "c2", Body: adfDocWithMedia("shared-file"), ServiceDeskPublic: &public},
				}},
			}},
			description:     nil,
			serviceDesk:     true,
			wantNotInternal: []string{"shared-file"},
		},
		{
			name: "non service desk import keeps every attachment visible",
			issue: &jira.JiraIssue{Fields: jira.JiraIssueFields{
				Attachment: jiraAttachmentIDs("any-file"),
				Comment:    &jira.JiraCommentContainer{Comments: []jira.JiraComment{}},
			}},
			description:     nil,
			serviceDesk:     false,
			wantNotInternal: []string{"any-file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := jiraInternalAttachmentIDs(tt.issue, tt.description, tt.serviceDesk)
			for _, id := range tt.wantInternal {
				if !got[id] {
					t.Errorf("attachment %q = visible, want internal", id)
				}
			}
			for _, id := range tt.wantNotInternal {
				if got[id] {
					t.Errorf("attachment %q = internal, want visible", id)
				}
			}
		})
	}
}
