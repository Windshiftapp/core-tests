//go:build test

package customfields_test

import (
	"errors"
	"reflect"
	"testing"

	"windshift/internal/customfields"
	"windshift/internal/testutils"
)

func seedPortalAndOrgFields(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	rows := []struct {
		id     int
		name   string
		portal bool
		org    bool
	}{
		{1, "Account tier", true, false},
		{2, "Priority", true, false},
		{3, "Priority", true, false},
		{4, "Org field", false, true},
	}
	for _, r := range rows {
		if _, err := tdb.Exec(
			`INSERT INTO custom_field_definitions (id, name, field_type, applies_to_portal_customers, applies_to_customer_organisations) VALUES (?, ?, 'text', ?, ?)`,
			r.id, r.name, r.portal, r.org,
		); err != nil {
			t.Fatalf("seed field %d: %v", r.id, err)
		}
	}
}

func TestCustomFieldNameIndex_Resolve(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	seedPortalAndOrgFields(t, tdb)

	index, err := customfields.LoadCustomFieldNameIndex(tdb, customfields.AppliesToPortalCustomers)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}

	cfv := map[string]any{"Account tier": "gold", "Priority": 2, "Unknown": "x"}
	changed, ambiguous := index.Resolve(cfv)
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !reflect.DeepEqual(ambiguous, []string{"Priority"}) {
		t.Fatalf("ambiguous = %v, want [Priority]", ambiguous)
	}
	want := map[string]any{"1": "gold", "Priority": 2, "Unknown": "x"}
	if !reflect.DeepEqual(cfv, want) {
		t.Fatalf("cfv = %v, want %v", cfv, want)
	}
}

func TestCustomFieldNameIndex_ValidateRejectsAmbiguous(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	seedPortalAndOrgFields(t, tdb)

	index, err := customfields.LoadCustomFieldNameIndex(tdb, customfields.AppliesToPortalCustomers)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}

	// Unique name is rewritten, unknown name dropped.
	cfv := map[string]any{"Account tier": "gold", "Unknown": "x"}
	if err := index.Validate(cfv); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !reflect.DeepEqual(cfv, map[string]any{"1": "gold"}) {
		t.Fatalf("cfv = %v, want {1: gold}", cfv)
	}

	// Ambiguous name is rejected.
	ambiguous := map[string]any{"Priority": 2}
	err = index.Validate(ambiguous)
	var nameErr *customfields.AmbiguousNameError
	if !errors.As(err, &nameErr) || nameErr.Name != "Priority" {
		t.Fatalf("err = %v, want *AmbiguousNameError{Priority}", err)
	}
}

func TestCustomFieldNameIndex_RebuildMirrors(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	seedPortalAndOrgFields(t, tdb)

	index, err := customfields.LoadCustomFieldNameIndex(tdb, customfields.AppliesToPortalCustomers)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}

	// Unique name gets a mirror from the authoritative id key.
	cfv := map[string]any{"1": "gold"}
	index.RebuildMirrors(cfv)
	if !reflect.DeepEqual(cfv, map[string]any{"1": "gold", "Account tier": "gold"}) {
		t.Fatalf("cfv = %v, want id + mirror", cfv)
	}

	// Duplicate name gets no mirror.
	dup := map[string]any{"2": 2}
	index.RebuildMirrors(dup)
	if !reflect.DeepEqual(dup, map[string]any{"2": 2}) {
		t.Fatalf("duplicate-name cfv = %v, want unchanged", dup)
	}

	// A stale mirror is removed when the authoritative id key is gone.
	stale := map[string]any{"Account tier": "stale"}
	index.RebuildMirrors(stale)
	if len(stale) != 0 {
		t.Fatalf("stale cfv = %v, want empty", stale)
	}

	// An ambiguous name key is left alone.
	ambiguous := map[string]any{"Priority": 2}
	index.RebuildMirrors(ambiguous)
	if !reflect.DeepEqual(ambiguous, map[string]any{"Priority": 2}) {
		t.Fatalf("ambiguous cfv = %v, want unchanged", ambiguous)
	}
}

func TestCustomFieldNameIndex_NameAndApplicability(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	seedPortalAndOrgFields(t, tdb)

	portal, err := customfields.LoadCustomFieldNameIndex(tdb, customfields.AppliesToPortalCustomers)
	if err != nil {
		t.Fatalf("load portal index: %v", err)
	}
	if name, ok := portal.Name(1); !ok || name != "Account tier" {
		t.Fatalf("portal.Name(1) = %q, %v", name, ok)
	}
	if _, ok := portal.Name(4); ok {
		t.Fatal("org-only field must not be in the portal index")
	}

	org, err := customfields.LoadCustomFieldNameIndex(tdb, customfields.AppliesToCustomerOrganisations)
	if err != nil {
		t.Fatalf("load org index: %v", err)
	}
	if name, ok := org.Name(4); !ok || name != "Org field" {
		t.Fatalf("org.Name(4) = %q, %v", name, ok)
	}
	if _, ok := org.Name(1); ok {
		t.Fatal("portal-only field must not be in the org index")
	}
}
