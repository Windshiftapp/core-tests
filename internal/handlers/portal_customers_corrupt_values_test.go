package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/services"
)

func TestGetPortalCustomersLogsCorruptCustomFieldValues(t *testing.T) {
	db := newNegativeTestDB(t)
	if db.GetDriverName() != "sqlite" {
		t.Skip("PostgreSQL JSONB rejects corrupt JSON at write time")
	}
	seedNegativeTestUser(t, db, 1)
	if _, err := db.Exec(`
		INSERT INTO portal_customers (id, name, email, custom_field_values)
		VALUES (501, 'Corrupt customer', 'corrupt@example.com', '{')
	`); err != nil {
		t.Fatalf("seed corrupt portal customer: %v", err)
	}

	permissionService := newNegativeTestPermissionService(t, db)
	timePermissionService := services.NewTimePermissionService(db, permissionService)
	handler := NewPortalCustomersHandler(
		db,
		permissionService,
		services.NewCustomerOrganisationPermissionService(db, permissionService, timePermissionService),
	)

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	req := authedRequest(http.MethodGet, "/api/portal/customers", 1, nil)
	rr := httptest.NewRecorder()
	handler.GetPortalCustomers(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	output := logs.String()
	if !strings.Contains(output, "failed to parse custom field values for customer") || !strings.Contains(output, `"customer_id":501`) {
		t.Fatalf("log output = %q, want parse warning with customer id", output)
	}
}
