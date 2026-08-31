package wscli

import (
	"strings"
	"testing"
	"time"
)

func itemWithAttachments(atts []Attachment) *Item {
	return &Item{
		Key:         "WI-123",
		Title:       "Mockup review",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Attachments: atts,
	}
}

// TestTaskGet_ImageAttachmentHint verifies the hint lists image attachments
// with their ids and points at view_image (WI-492).
func TestTaskGet_ImageAttachmentHint(t *testing.T) {
	item := itemWithAttachments([]Attachment{
		{ID: 7, OriginalFilename: "mockup.png", MimeType: "image/png"},
		{ID: 9, OriginalFilename: "diagram.svg", MimeType: "image/svg+xml"},
		{ID: 4, OriginalFilename: "spec.pdf", MimeType: "application/pdf"},
	})
	out := captureStdout(t, func() {
		(&Output{format: "table"}).Print(item)
	})
	for _, want := range []string{"2 image attachments", "view_image", "id 7", "mockup.png", "id 9"} {
		if !strings.Contains(out, want) {
			t.Errorf("hint missing %q\n----\n%s", want, out)
		}
	}
	// The non-image PDF must not be listed as an image attachment.
	if strings.Contains(out, "id 4") || strings.Contains(out, "spec.pdf") {
		t.Errorf("non-image attachment should not appear in the image hint:\n%s", out)
	}
}

// TestTaskGet_SingularImageHint checks the singular noun for one image.
func TestTaskGet_SingularImageHint(t *testing.T) {
	item := itemWithAttachments([]Attachment{
		{ID: 1, OriginalFilename: "shot.jpg", MimeType: "image/jpeg"},
	})
	out := captureStdout(t, func() {
		(&Output{format: "table"}).Print(item)
	})
	// Pin the singular noun without pinning surrounding punctuation — the
	// hint copy has been reworded before while the noun logic stayed put.
	if !strings.Contains(out, "1 image attachment") || strings.Contains(out, "image attachments") {
		t.Errorf("expected singular noun, got:\n%s", out)
	}
}

// TestTaskGet_NoHintWithoutImages verifies items with no image attachments emit
// no hint at all (non-image attachments alone must not trigger it).
func TestTaskGet_NoHintWithoutImages(t *testing.T) {
	item := itemWithAttachments([]Attachment{
		{ID: 4, OriginalFilename: "spec.pdf", MimeType: "application/pdf"},
	})
	out := captureStdout(t, func() {
		(&Output{format: "table"}).Print(item)
	})
	if strings.Contains(out, "view_image") {
		t.Errorf("no image hint expected for non-image attachments:\n%s", out)
	}
}
