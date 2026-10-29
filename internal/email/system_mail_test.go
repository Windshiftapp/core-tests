//go:build test

package email_test

import (
	"testing"

	"windshift/internal/email"
)

func systemMailTestEmail(subject, body string, from email.EmailAddress, headers map[string][]string) *email.ParsedEmail {
	return &email.ParsedEmail{
		MessageID:  "<system-mail-test@example.com>",
		Subject:    subject,
		PlainBody:  body,
		From:       from,
		RawHeaders: headers,
	}
}

func TestClassifySystemMail(t *testing.T) {
	customer := email.EmailAddress{Name: "Customer", Address: "customer@example.com"}

	tests := []struct {
		name         string
		message      *email.ParsedEmail
		wantCategory email.SystemMailCategory
		wantMatch    bool
	}{
		{
			name: "delivery status report content type",
			message: systemMailTestEmail(
				"Something went wrong",
				"see attachment",
				email.EmailAddress{Address: "noreply@relay.example.com"},
				map[string][]string{"Content-Type": {"multipart/report; report-type=delivery-status; boundary=x"}},
			),
			wantCategory: email.SystemMailDeliveryFailure,
			wantMatch:    true,
		},
		{
			name: "mailer daemon sender",
			message: systemMailTestEmail(
				"Something went wrong",
				"no useful body",
				email.EmailAddress{Address: "MAILER-DAEMON@mx.example.com"},
				nil,
			),
			wantCategory: email.SystemMailDeliveryFailure,
			wantMatch:    true,
		},
		{
			name: "undeliverable subject",
			message: systemMailTestEmail(
				"Undeliverable: Windshift — You have 9 new notifications",
				"Delivery has failed to these recipients.",
				email.EmailAddress{Address: "postmaster@outlook.com"},
				nil,
			),
			wantCategory: email.SystemMailDeliveryFailure,
			wantMatch:    true,
		},
		{
			name: "failed to deliver subject from plain sender",
			message: systemMailTestEmail(
				"Failed to deliver message",
				"Your message could not be delivered to the following recipients.",
				customer,
				nil,
			),
			wantCategory: email.SystemMailDeliveryFailure,
			wantMatch:    true,
		},
		{
			name: "body only delivery failure",
			message: systemMailTestEmail(
				"Returned mail",
				"Diagnostic information for administrators:\n\nRemote server returned 550",
				customer,
				nil,
			),
			wantCategory: email.SystemMailDeliveryFailure,
			wantMatch:    true,
		},
		{
			name: "auto submitted auto replied",
			message: systemMailTestEmail(
				"Re: your request",
				"I am on vacation.",
				customer,
				map[string][]string{"Auto-Submitted": {"auto-replied"}},
			),
			wantCategory: email.SystemMailAutoReply,
			wantMatch:    true,
		},
		{
			name: "out of office subject",
			message: systemMailTestEmail(
				"Automatic reply: Out of office",
				"back next week",
				customer,
				nil,
			),
			wantCategory: email.SystemMailAutoReply,
			wantMatch:    true,
		},
		{
			name: "normal customer mail is not system mail",
			message: systemMailTestEmail(
				"Re: invoice question",
				"Could you resend the invoice please?",
				customer,
				nil,
			),
			wantMatch: false,
		},
		{
			name: "auto-generated notification is left alone",
			message: systemMailTestEmail(
				"Build failed on main",
				"Pipeline #42 failed.",
				email.EmailAddress{Address: "ci@example.com"},
				map[string][]string{"Auto-Submitted": {"auto-generated"}},
			),
			wantMatch: false,
		},
		{
			name: "bulk precedence is left alone",
			message: systemMailTestEmail(
				"Monthly newsletter",
				"Here is what happened this month.",
				email.EmailAddress{Address: "news@example.com"},
				map[string][]string{"Precedence": {"bulk"}},
			),
			wantMatch: false,
		},
		{
			name:         "nil message",
			message:      nil,
			wantMatch:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, matched := email.ClassifySystemMail(tc.message)
			if matched != tc.wantMatch {
				t.Fatalf("matched = %v, want %v (info %+v)", matched, tc.wantMatch, info)
			}
			if !tc.wantMatch {
				return
			}
			if info.Category != tc.wantCategory {
				t.Fatalf("category = %q, want %q", info.Category, tc.wantCategory)
			}
			if info.Detail == "" {
				t.Fatal("expected a non-empty detail explaining the match")
			}
		})
	}
}
