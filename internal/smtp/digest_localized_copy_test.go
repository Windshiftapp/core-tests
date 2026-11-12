//go:build test

package smtp

import (
	"strings"
	"testing"
	"time"

	"windshift/internal/emailutil"
	"windshift/internal/models"
)

// TestDigestRendersLocalizedNotificationCopy covers the digest re-rendering a
// notification's stored English title/message in the recipient's locale when
// the row carries i18n metadata.
func TestDigestRendersLocalizedNotificationCopy(t *testing.T) {
	sender := &NotificationSMTPSender{}
	notifications := []models.Notification{{
		Title:     "You were mentioned",
		Message:   "Sam mentioned you in a comment on Cannot log in (OPS-7)",
		Type:      "mention",
		Timestamp: time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC),
		Metadata: models.NotificationMetadataJSON(
			"notification.mention.title",
			"notification.mention.comment",
			map[string]any{"actor": "Sam", "itemTitle": "Cannot log in", "itemKey": "OPS-7"},
		),
	}}

	_, htmlBody, textBody, err := sender.RenderEmailLocale(
		"de", emailutil.TemplateNotificationBatch,
		buildNotificationBatchData("https://app.example.com", "Alex", "de", "UTC", notifications),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale: %v", err)
	}
	for _, want := range []string{
		"Sie wurden erwähnt",
		"Sam hat Sie in einem Kommentar zu Cannot log in (OPS-7) erwähnt",
	} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("plain-text digest missing %q:\n%s", want, textBody)
		}
	}
	if strings.Contains(textBody, "mentioned you in a comment") {
		t.Fatalf("digest still carries the stored English copy:\n%s", textBody)
	}
	if !strings.Contains(htmlBody, "Sie wurden erwähnt") {
		t.Fatalf("HTML digest is not localized:\n%s", htmlBody)
	}
}

// TestDigestFallsBackToStoredCopyWithoutMetadata proves rule-based and
// user-authored notifications (which carry no i18n metadata) render their
// stored text unchanged.
func TestDigestFallsBackToStoredCopyWithoutMetadata(t *testing.T) {
	sender := &NotificationSMTPSender{}
	notifications := []models.Notification{{
		Title:     "Rule-based notice",
		Message:   "This text is authored by an admin.",
		Type:      "info",
		Timestamp: time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC),
	}}

	_, _, textBody, err := sender.RenderEmailLocale(
		"de", emailutil.TemplateNotificationBatch,
		buildNotificationBatchData("https://app.example.com", "Alex", "de", "UTC", notifications),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale: %v", err)
	}
	if !strings.Contains(textBody, "Rule-based notice") || !strings.Contains(textBody, "authored by an admin") {
		t.Fatalf("stored copy was not preserved:\n%s", textBody)
	}
}

// TestDigestLocalizedCopyFallsBackWhenTranslationMissing ensures a row with an
// unknown i18n key still shows its stored copy instead of the raw key.
func TestDigestLocalizedCopyFallsBackWhenTranslationMissing(t *testing.T) {
	sender := &NotificationSMTPSender{}
	notifications := []models.Notification{{
		Title:     "Stored title",
		Message:   "Stored message",
		Type:      "info",
		Timestamp: time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC),
		Metadata:  models.NotificationMetadataJSON("notification.unknown.title", "notification.unknown.message", nil),
	}}

	_, _, textBody, err := sender.RenderEmailLocale(
		"de", emailutil.TemplateNotificationBatch,
		buildNotificationBatchData("https://app.example.com", "Alex", "de", "UTC", notifications),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale: %v", err)
	}
	if !strings.Contains(textBody, "Stored title") || !strings.Contains(textBody, "Stored message") {
		t.Fatalf("missing translation did not fall back to stored copy:\n%s", textBody)
	}
	if strings.Contains(textBody, "notification.unknown") {
		t.Fatalf("digest leaked a raw i18n key:\n%s", textBody)
	}
}
