package services

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/emailutil"
)

// capturingMagicLinkSender records the template payload so tests can assert
// the verify URL the customer receives without touching SMTP.
type capturingMagicLinkSender struct {
	templateName string
	data         any
}

func (c *capturingMagicLinkSender) IsSMTPConfigured() bool { return true }

func (c *capturingMagicLinkSender) SendTransactional(_ string, templateName string, data any) error {
	c.templateName = templateName
	c.data = data
	return nil
}

func TestSendMagicLinkEmailEmbedsValidatedNextDestination(t *testing.T) {
	tests := []struct {
		name string
		next string
		want string
	}{
		{
			name: "without next the fragment carries only the token",
			next: "",
			want: "https://portal.example.com/portal/acme/verify#token=tok",
		},
		{
			name: "query separators are encoded so the fragment stays parseable",
			next: "/portal/acme?view=requests&id=42&prefill.title=a b",
			want: "https://portal.example.com/portal/acme/verify#token=tok&next=%2Fportal%2Facme%3Fview%3Drequests%26id%3D42%26prefill.title%3Da%20b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &capturingMagicLinkSender{}
			service := NewMagicLinkService(nil, sender, "https://portal.example.com")

			if err := service.SendMagicLinkEmail("customer@example.com", "Customer", "tok", "acme", tc.next); err != nil {
				t.Fatalf("SendMagicLinkEmail: %v", err)
			}
			if sender.templateName != emailutil.TemplateMagicLink {
				t.Fatalf("template = %q, want %q", sender.templateName, emailutil.TemplateMagicLink)
			}
			payload, ok := sender.data.(struct {
				FirstName    string
				MagicLinkURL string
				ExpiresIn    string
			})
			if !ok {
				t.Fatalf("unexpected payload type %T", sender.data)
			}
			if payload.MagicLinkURL != tc.want {
				t.Fatalf("MagicLinkURL = %q, want %q", payload.MagicLinkURL, tc.want)
			}
		})
	}
}

func TestPortalNextPathAcceptsOnlySamePortalPaths(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty is dropped", raw: "", want: ""},
		{name: "same portal home is kept", raw: "/portal/acme", want: "/portal/acme"},
		{name: "same portal nested path is kept", raw: "/portal/acme/approvals/17", want: "/portal/acme/approvals/17"},
		{name: "same portal query is kept", raw: "/portal/acme?view=requests&id=42", want: "/portal/acme?view=requests&id=42"},
		{name: "whitespace is trimmed", raw: "  /portal/acme/kb  ", want: "/portal/acme/kb"},
		{name: "other portal is rejected", raw: "/portal/other/approvals/17", want: ""},
		{name: "prefix collision is rejected", raw: "/portal/acmex", want: ""},
		{name: "external URL is rejected", raw: "https://evil.example.com/", want: ""},
		{name: "protocol relative is rejected", raw: "//evil.example.com/portal/acme", want: ""},
		{name: "root is rejected", raw: "/", want: ""},
		{name: "non portal path is rejected", raw: "/admin", want: ""},
		{name: "over long value is dropped", raw: "/portal/acme/" + strings.Repeat("a", 2048), want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PortalNextPath(tc.raw, "acme"); got != tc.want {
				t.Fatalf("PortalNextPath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestFindOrCreatePortalCustomerGrantsChannelAccessIdempotently(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "magic-link-customer.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction)
		VALUES ('Portal', 'portal', 'inbound') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	service := NewMagicLinkService(db, nil, "")
	firstID, err := service.FindOrCreatePortalCustomer("  Customer@Example.com ", "Customer", channelID)
	if err != nil {
		t.Fatalf("first FindOrCreatePortalCustomer: %v", err)
	}
	secondID, err := service.FindOrCreatePortalCustomer("customer@example.com", "Ignored", channelID)
	if err != nil {
		t.Fatalf("second FindOrCreatePortalCustomer: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate lookup returned customer %d, want %d", secondID, firstID)
	}

	var storedEmail string
	if err := db.QueryRow(`SELECT email FROM portal_customers WHERE id = ?`, firstID).Scan(&storedEmail); err != nil {
		t.Fatalf("load customer: %v", err)
	}
	if storedEmail != "customer@example.com" {
		t.Fatalf("stored email = %q, want normalized address", storedEmail)
	}
	var grants int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM portal_customer_channels
		WHERE portal_customer_id = ? AND channel_id = ?
	`, firstID, channelID).Scan(&grants); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grants != 1 {
		t.Fatalf("channel grants = %d, want 1", grants)
	}
}

func TestGetPortalCustomerByEmailIsCaseInsensitive(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "magic-link-lookup.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction)
		VALUES ('Portal', 'portal', 'inbound') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	service := NewMagicLinkService(db, nil, "")
	customerID, err := service.FindOrCreatePortalCustomer("Customer@Example.com", "Customer", channelID)
	if err != nil {
		t.Fatalf("FindOrCreatePortalCustomer: %v", err)
	}

	// The stored address is normalized to lowercase; lookups with any casing
	// must still resolve the same customer and name.
	gotID, name, err := service.GetPortalCustomerByEmail("CUSTOMER@example.COM")
	if err != nil {
		t.Fatalf("GetPortalCustomerByEmail: %v", err)
	}
	if gotID != customerID || name != "Customer" {
		t.Fatalf("lookup returned (%d, %q), want (%d, %q)", gotID, name, customerID, "Customer")
	}

	if _, _, err := service.GetPortalCustomerByEmail("missing@example.com"); !errors.Is(err, ErrPortalCustomerNotFound) {
		t.Fatalf("missing email error = %v, want ErrPortalCustomerNotFound", err)
	}
}
