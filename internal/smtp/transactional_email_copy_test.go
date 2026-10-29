//go:build test

package smtp

import (
	"strings"
	"testing"

	"windshift/internal/emailutil"
)

// TestTransactionalEmailCopyStatesRealExpiry pins each token email's stated
// validity to the value the sender passes, so the copy cannot drift from the
// constant that enforces it again.
func TestTransactionalEmailCopyStatesRealExpiry(t *testing.T) {
	sender := &NotificationSMTPSender{}
	cases := []struct {
		name     string
		template string
		want     string
	}{
		{"magic link", emailutil.TemplateMagicLink, "30 minutes"},
		{"approval requested", emailutil.TemplateApprovalRequested, "24 hours"},
		{"email verification", emailutil.TemplateEmailVerification, "24 hours"},
		{"invitation", emailutil.TemplateInvitation, "7 days"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, htmlBody, textBody, err := sender.RenderEmail(tc.template, emailutil.SampleData(tc.template))
			if err != nil {
				t.Fatalf("RenderEmail(%s): %v", tc.template, err)
			}
			if !strings.Contains(textBody, tc.want) {
				t.Fatalf("plain-text body does not state %q: %q", tc.want, textBody)
			}
			if !strings.Contains(htmlBody, tc.want) {
				t.Fatalf("HTML body does not state %q: %q", tc.want, htmlBody)
			}
			if strings.Contains(textBody, "15 minutes") || strings.Contains(htmlBody, "15 minutes") {
				t.Fatalf("email still claims a 15-minute validity")
			}
		})
	}
}

// TestPortalReplyFooterDoesNotContradictReplyInstruction guards the customer
// instruction: the body asks the customer to reply, so the shell footer must
// not tell them not to.
func TestPortalReplyFooterDoesNotContradictReplyInstruction(t *testing.T) {
	sender := &NotificationSMTPSender{}
	_, htmlBody, textBody, err := sender.RenderEmail(
		emailutil.TemplatePortalReply,
		emailutil.SampleData(emailutil.TemplatePortalReply),
	)
	if err != nil {
		t.Fatalf("RenderEmail: %v", err)
	}
	if !strings.Contains(htmlBody, "simply reply to this email") {
		t.Fatalf("portal reply lost its reply instruction: %q", htmlBody)
	}
	if !strings.Contains(htmlBody, "open request with us") {
		t.Fatalf("portal reply is missing the repliable footer: %q", htmlBody)
	}
	if strings.Contains(htmlBody, "please do not reply") || strings.Contains(textBody, "please do not reply") {
		t.Fatalf("portal reply tells the customer not to reply: html=%q text=%q", htmlBody, textBody)
	}
}
