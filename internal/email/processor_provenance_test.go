//go:build test

package email

import (
	"context"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestProcessorFindOrCreateRecordsEmailIntakeProvenance pins the WI-1553
// intake path: customers auto-created by inbound email carry
// created_via='email-intake', while known senders keep their original row.
func TestProcessorFindOrCreateRecordsEmailIntakeProvenance(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	ctx := context.Background()

	var channelID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO channels (name, type, direction, status) VALUES ('intake', 'email', 'inbound', 'enabled') RETURNING id`,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	processor := NewProcessor(db, blobstore.NewLocal(t.TempDir()))

	customerID, err := processor.findOrCreatePortalCustomer(ctx,
		"spamsender@test.local", "spam sender", int(channelID), &models.ChannelConfig{})
	if err != nil {
		t.Fatalf("find or create: %v", err)
	}

	var createdVia string
	if err := db.QueryRowContext(ctx,
		`SELECT created_via FROM portal_customers WHERE id = ?`, customerID,
	).Scan(&createdVia); err != nil {
		t.Fatalf("read created_via: %v", err)
	}
	if createdVia != models.CustomerCreatedViaEmailIntake {
		t.Fatalf("created_via = %q, want %q", createdVia, models.CustomerCreatedViaEmailIntake)
	}

	// A known sender is found, not recreated — provenance stays as it was.
	againID, err := processor.findOrCreatePortalCustomer(ctx,
		"spamsender@test.local", "spam sender", int(channelID), &models.ChannelConfig{})
	if err != nil {
		t.Fatalf("second find or create: %v", err)
	}
	if againID != customerID {
		t.Fatalf("second call returned customer %d, want existing %d", againID, customerID)
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_customers WHERE email = ?`, "spamsender@test.local",
	).Scan(&count); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	if count != 1 {
		t.Fatalf("customers with the sender email = %d, want 1", count)
	}
}
