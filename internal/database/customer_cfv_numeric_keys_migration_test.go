//go:build test

package database_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

const customerCFVNumericKeysVersion = "20261120_customer_cfv_numeric_keys"

// TestMigration_CustomerCFVNumericKeys_NoDataLoss starts from a 0.8.5
// checkpoint, seeds the pre-migration name-keyed shapes (including duplicate
// names, an orphan name, an already-id-keyed row, and — on SQLite — corrupt
// JSON), runs only the migration, and asserts every value survives.
func TestMigration_CustomerCFVNumericKeys_NoDataLoss(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)
	seedCustomerCFVFixture(t, tdb)
	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, customerCFVNumericKeysVersion, "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run customer cfv migration: %v", err)
	}

	// Unique name -> id key added, name mirror kept, value preserved.
	assertCFV(t, tdb, "portal_customers", 1, `{"10":"gold","Account tier":"gold","Priority":2}`)
	// Already id-keyed -> mirror refreshed from the authoritative value.
	assertCFV(t, tdb, "portal_customers", 2, `{"10":"silver","Account tier":"silver"}`)
	// Unknown/orphan name -> left untouched.
	assertCFV(t, tdb, "portal_customers", 3, `{"Unknown field":"x"}`)
	// Empty object -> unchanged.
	assertCFV(t, tdb, "portal_customers", 5, `{}`)
	// Duplicate name -> no mirror, existing id key untouched.
	assertCFV(t, tdb, "portal_customers", 6, `{"Priority":3,"12":3}`)

	// Applicability is scoped: "Account tier" only applies to portal customers,
	// so the organisation row keeps the orphan name key.
	assertCFV(t, tdb, "customer_organisations", 1, `{"13":"acme","Org field":"acme"}`)
	assertCFV(t, tdb, "customer_organisations", 2, `{"Account tier":"nope"}`)

	if !testutils.IsPostgres() {
		// Malformed JSON must survive untouched (Postgres JSONB rejects it at
		// write time, so this row only exists on SQLite).
		var raw string
		if err := tdb.QueryRow(`SELECT custom_field_values FROM portal_customers WHERE id = 4`).Scan(&raw); err != nil {
			t.Fatalf("read corrupt row: %v", err)
		}
		if raw != "{" {
			t.Fatalf("corrupt row = %q, want %q", raw, "{")
		}
	}

	// Idempotency: a second Initialize must not change anything.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	assertCFV(t, tdb, "portal_customers", 1, `{"10":"gold","Account tier":"gold","Priority":2}`)
	assertCFV(t, tdb, "customer_organisations", 1, `{"13":"acme","Org field":"acme"}`)
}

func seedCustomerCFVFixture(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	jsonColumn := "TEXT"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
		jsonColumn = "JSONB"
	}
	for _, stmt := range []string{
		`CREATE TABLE custom_field_definitions (
			id ` + idColumn + `,
			name TEXT NOT NULL,
			applies_to_portal_customers BOOLEAN DEFAULT false,
			applies_to_customer_organisations BOOLEAN DEFAULT false
		)`,
		`CREATE TABLE portal_customers (
			id ` + idColumn + `,
			name TEXT NOT NULL,
			email TEXT,
			custom_field_values ` + jsonColumn + `
		)`,
		`CREATE TABLE customer_organisations (
			id ` + idColumn + `,
			name TEXT NOT NULL,
			custom_field_values ` + jsonColumn + `
		)`,
	} {
		if _, err := tdb.Exec(stmt); err != nil {
			t.Fatalf("create fixture table: %v", err)
		}
	}

	fields := []struct {
		id     int
		name   string
		portal bool
		org    bool
	}{
		{10, "Account tier", true, false},
		{11, "Priority", true, false},
		{12, "Priority", true, false},
		{13, "Org field", false, true},
		{14, "Old name", true, false},
	}
	for _, f := range fields {
		if _, err := tdb.Exec(
			`INSERT INTO custom_field_definitions (id, name, applies_to_portal_customers, applies_to_customer_organisations) VALUES (?, ?, ?, ?)`,
			f.id, f.name, f.portal, f.org,
		); err != nil {
			t.Fatalf("seed custom field %d: %v", f.id, err)
		}
	}

	portalRows := []struct {
		id  int
		cfv string
	}{
		{1, `{"Account tier":"gold","Priority":2}`},
		{2, `{"10":"silver"}`},
		{3, `{"Unknown field":"x"}`},
		{5, `{}`},
		{6, `{"Priority":3,"12":3}`},
	}
	for _, row := range portalRows {
		if _, err := tdb.Exec(
			`INSERT INTO portal_customers (id, name, email, custom_field_values) VALUES (?, ?, ?, ?)`,
			row.id, fmt.Sprintf("pc-%d", row.id), fmt.Sprintf("pc%d@example.com", row.id), row.cfv,
		); err != nil {
			t.Fatalf("seed portal customer %d: %v", row.id, err)
		}
	}
	if !testutils.IsPostgres() {
		if _, err := tdb.Exec(
			`INSERT INTO portal_customers (id, name, email, custom_field_values) VALUES (4, 'pc-4', 'pc4@example.com', '{')`,
		); err != nil {
			t.Fatalf("seed corrupt portal customer: %v", err)
		}
	}

	orgRows := []struct {
		id  int
		cfv string
	}{
		{1, `{"Org field":"acme"}`},
		{2, `{"Account tier":"nope"}`},
	}
	for _, row := range orgRows {
		if _, err := tdb.Exec(
			`INSERT INTO customer_organisations (id, name, custom_field_values) VALUES (?, ?, ?)`,
			row.id, fmt.Sprintf("co-%d", row.id), row.cfv,
		); err != nil {
			t.Fatalf("seed customer organisation %d: %v", row.id, err)
		}
	}
}

// assertCFV compares the stored custom_field_values object with wantJSON,
// parsing both with UseNumber so numeric values compare exactly.
func assertCFV(t *testing.T, tdb *testutils.TestDB, table string, id int, wantJSON string) {
	t.Helper()
	var raw string
	//nolint:gosec // table is a test-controlled literal
	if err := tdb.QueryRow(fmt.Sprintf(`SELECT custom_field_values FROM %s WHERE id = ?`, table), id).Scan(&raw); err != nil {
		t.Fatalf("read %s %d cfv: %v", table, id, err)
	}
	got := decodeJSONObject(t, raw)
	want := decodeJSONObject(t, wantJSON)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s %d cfv = %s, want %s", table, id, raw, wantJSON)
	}
}

func decodeJSONObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("decode json %q: %v", raw, err)
	}
	return out
}
