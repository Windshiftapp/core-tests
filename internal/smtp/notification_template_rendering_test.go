//go:build test

package smtp

import (
	"strings"
	"testing"
	"time"

	"windshift/internal/emailutil"
	"windshift/internal/models"
)

func TestRenderEmailKeepsTextLiteralAndEscapesHTML(t *testing.T) {
	sender := &NotificationSMTPSender{}

	t.Run("notification batch", func(t *testing.T) {
		notifications := []models.Notification{{
			Title:     "R&D <launch> O'Hara — 東京",
			Message:   "Review https://example.test/a?one=1&two=2 <script>alert('x')</script>",
			Type:      "comment",
			Timestamp: time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC),
		}}

		subject, htmlBody, textBody, err := sender.RenderEmail(
			emailutil.TemplateNotificationBatch,
			buildNotificationBatchData("https://app.example.com", "D'Arcy & Sons <ops>", notifications),
		)
		if err != nil {
			t.Fatalf("RenderEmail: %v", err)
		}

		if got, want := subject, "Windshift — You have 1 new notification"; got != want {
			t.Fatalf("subject = %q, want %q", got, want)
		}
		wantText := "Hi D'Arcy & Sons <ops>,\n\n" +
			"You have 1 new notification from Windshift:\n\n" +
			"* R&D <launch> O'Hara — 東京\n" +
			"  Review https://example.test/a?one=1&two=2 <script>alert('x')</script>\n" +
			"  September 5, 2026 at 2:30 PM\n\n\n" +
			"Manage your notification preferences in Windshift.\n"
		if textBody != wantText {
			t.Fatalf("text body mismatch\n got: %q\nwant: %q", textBody, wantText)
		}
		for _, want := range []string{
			"Hi D&#39;Arcy &amp; Sons &lt;ops&gt;",
			"R&amp;D &lt;launch&gt; O&#39;Hara — 東京",
			"Review https://example.test/a?one=1&amp;two=2 &lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt;",
		} {
			if !strings.Contains(htmlBody, want) {
				t.Fatalf("HTML body missing escaped value %q", want)
			}
		}
		if strings.Contains(htmlBody, "<script>alert('x')</script>") {
			t.Fatal("HTML body contains executable injected markup")
		}
	})

	t.Run("threaded portal reply", func(t *testing.T) {
		data := struct {
			AuthorName      string
			ItemKey         string
			ItemTitle       string
			Content         string
			OriginalSubject string
		}{
			AuthorName:      "D'Arcy & Sons",
			ItemKey:         "OPS-42",
			ItemTitle:       "R&D <launch> — 東京",
			Content:         "Open https://example.test/reply?one=1&two=2\n<script>alert('x')</script>",
			OriginalSubject: "R&D <launch> O'Hara — 東京",
		}

		subject, htmlBody, textBody, err := sender.RenderEmail(emailutil.TemplatePortalReply, data)
		if err != nil {
			t.Fatalf("RenderEmail: %v", err)
		}
		if got, want := subject, "Re: R&D <launch> O'Hara — 東京"; got != want {
			t.Fatalf("subject = %q, want %q", got, want)
		}
		wantText := "D'Arcy & Sons replied on OPS-42 · R&D <launch> — 東京\n\n" +
			"─────────────────────────────\n" +
			"Open https://example.test/reply?one=1&two=2\n<script>alert('x')</script>\n" +
			"─────────────────────────────\n\n" +
			"To continue the conversation, simply reply to this email.\n"
		if textBody != wantText {
			t.Fatalf("text body mismatch\n got: %q\nwant: %q", textBody, wantText)
		}
		for _, want := range []string{
			"<title>Re: R&amp;D &lt;launch&gt; O&#39;Hara — 東京</title>",
			"D&#39;Arcy &amp; Sons",
			"R&amp;D &lt;launch&gt; — 東京",
			`href="https://example.test/reply?one=1&amp;two=2"`,
			"&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt;",
		} {
			if !strings.Contains(htmlBody, want) {
				t.Fatalf("HTML body missing rendered value %q", want)
			}
		}
		if strings.Contains(htmlBody, "<script>alert('x')</script>") {
			t.Fatal("HTML body contains executable injected markup")
		}
	})

	t.Run("approval URL", func(t *testing.T) {
		data := struct {
			FirstName   string
			ItemKey     string
			ItemTitle   string
			ApprovalURL string
			ExpiresIn   string
		}{
			FirstName:   "Zoë & Co",
			ItemKey:     "OPS-43",
			ItemTitle:   "Budget <Q4> O'Hara — 東京",
			ApprovalURL: "https://example.test/approve?token=a&next=%2Fitems%2F43&mode=review",
			ExpiresIn:   "24 hours",
		}

		subject, htmlBody, textBody, err := sender.RenderEmail(emailutil.TemplateApprovalRequested, data)
		if err != nil {
			t.Fatalf("RenderEmail: %v", err)
		}
		if got, want := subject, "Approval requested: OPS-43 — Budget <Q4> O'Hara — 東京"; got != want {
			t.Fatalf("subject = %q, want %q", got, want)
		}
		if !strings.Contains(textBody, "https://example.test/approve?token=a&next=%2Fitems%2F43&mode=review") {
			t.Fatalf("plain-text URL was changed: %q", textBody)
		}
		if !strings.Contains(htmlBody, `href="https://example.test/approve?token=a&amp;next=%2Fitems%2F43&amp;mode=review"`) {
			t.Fatalf("HTML URL was not attribute-escaped: %q", htmlBody)
		}
		if !strings.Contains(textBody, "valid for 24 hours") {
			t.Fatalf("approval email states the wrong validity: %q", textBody)
		}
	})
}
