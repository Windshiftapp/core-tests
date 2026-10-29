//go:build test

package email_test

import (
	"context"
	"path/filepath"
	"testing"

	"windshift/internal/email"
)

// TestProcessor_AttachmentCountCap enforces the WI-1142 abuse bound: a
// single inbound email can never create more than 25 attachment records, no
// matter how many attachments arrive.
func TestProcessor_AttachmentCountCap(t *testing.T) {
	env := setupProcessorTestEnv(t, true)
	ctx := context.Background()

	const total = 30
	const cap = 25
	attachments := make([]email.Attachment, 0, total)
	for i := range total {
		attachments = append(attachments, email.Attachment{
			Filename:    filepath.Join("bulk", "file-"+string(rune('a'+i))+".txt"),
			ContentType: "text/plain",
			Size:        32,
			Data:        make([]byte, 32),
		})
	}

	pe := newTestEmail("<att-cap@example.com>", "Attachment flood", "Too many files.")
	pe.Attachments = attachments

	result, err := env.processor.ProcessEmail(ctx, pe, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("ProcessEmail failed: %v", err)
	}

	var stored int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE item_id = ?`, *result.ItemID).Scan(&stored); err != nil {
		t.Fatalf("count stored attachments: %v", err)
	}
	if stored != cap {
		t.Fatalf("stored attachments = %d, want %d", stored, cap)
	}
}
