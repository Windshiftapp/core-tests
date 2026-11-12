package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// TestPortalCustomerCustomFieldValueKeys pins the WI-2025 contract on the
// session API: custom-field values are stored and returned by numeric field id,
// a legacy name key is accepted on write and kept as a rollback mirror, and an
// ambiguous duplicate name is rejected.
func TestPortalCustomerCustomFieldValueKeys(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	fieldName := "CFV Key Field " + ts()
	fieldID := createCustomerCustomField(t, server, map[string]interface{}{
		"name":                        fieldName,
		"field_type":                  "text",
		"applies_to_portal_customers": true,
	})
	fieldKey := strconv.Itoa(fieldID)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers", map[string]interface{}{
		"name":                "CFV Customer " + ts(),
		"email":               "cfv-customer-" + ts() + "@example.com",
		"custom_field_values": map[string]interface{}{fieldName: "gold"},
	})
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	customerID := ExtractIDFromResponse(t, created)

	// API response is id-keyed; the unique-name mirror is stripped.
	cfv, _ := created["custom_field_values"].(map[string]interface{})
	if cfv[fieldKey] != "gold" {
		t.Fatalf("response cfv[%s] = %v, want gold (cfv=%v)", fieldKey, cfv[fieldKey], cfv)
	}
	if _, ok := cfv[fieldName]; ok {
		t.Fatalf("response must strip the unique-name mirror, got %v", cfv)
	}

	// Stored row keeps the mirror for downgrade reads.
	stored := readStoredCFV(t, server, "portal_customers", customerID)
	if stored[fieldKey] != "gold" || stored[fieldName] != "gold" {
		t.Fatalf("stored cfv = %v, want id key %s and name mirror %q", stored, fieldKey, fieldName)
	}

	// A legacy name-keyed row (seeded directly, as a pre-0.9.4 install would
	// have) is returned id-keyed.
	legacyName := "CFV Legacy Field " + ts()
	legacyID := createCustomerCustomField(t, server, map[string]interface{}{
		"name":                        legacyName,
		"field_type":                  "text",
		"applies_to_portal_customers": true,
	})
	legacyEmail := "cfv-legacy-" + ts() + "@example.com"
	if _, err := server.DB().ExecWrite(
		`INSERT INTO portal_customers (name, email, custom_field_values) VALUES (?, ?, ?)`,
		"CFV Legacy "+ts(), legacyEmail, fmt.Sprintf(`{"%s":"silver"}`, legacyName),
	); err != nil {
		t.Fatalf("seed legacy name-keyed customer: %v", err)
	}
	var legacyCustomerID int
	if err := server.DB().QueryRow(`SELECT id FROM portal_customers WHERE email = ?`, legacyEmail).Scan(&legacyCustomerID); err != nil {
		t.Fatalf("resolve legacy customer: %v", err)
	}
	getResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/portal-customers/%d", legacyCustomerID), nil)
	AssertStatusCode(t, getResp, http.StatusOK)
	var fetched map[string]interface{}
	DecodeJSON(t, getResp, &fetched)
	fetchedCFV, _ := fetched["custom_field_values"].(map[string]interface{})
	if fetchedCFV[strconv.Itoa(legacyID)] != "silver" {
		t.Fatalf("legacy read cfv = %v, want id key %d = silver", fetchedCFV, legacyID)
	}
	if _, ok := fetchedCFV[legacyName]; ok {
		t.Fatalf("legacy read must strip the name key, got %v", fetchedCFV)
	}

	// Duplicate names make the name key ambiguous: a write using it is rejected.
	dupName := "CFV Dup " + ts()
	createCustomerCustomField(t, server, map[string]interface{}{
		"name":                        dupName,
		"field_type":                  "text",
		"applies_to_portal_customers": true,
	})
	createCustomerCustomField(t, server, map[string]interface{}{
		"name":                        dupName,
		"field_type":                  "text",
		"applies_to_portal_customers": true,
	})
	dupResp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers", map[string]interface{}{
		"name":                "CFV Dup Customer " + ts(),
		"email":               "cfv-dup-" + ts() + "@example.com",
		"custom_field_values": map[string]interface{}{dupName: "x"},
	})
	_ = dupResp.Body.Close()
	AssertStatusCode(t, dupResp, http.StatusBadRequest)
}

// TestCustomerOrganisationCustomFieldValueKeys pins the same contract on the
// customer-organisation update path.
func TestCustomerOrganisationCustomFieldValueKeys(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	fieldName := "CFV Org Field " + ts()
	fieldID := createCustomerCustomField(t, server, map[string]interface{}{
		"name":                              fieldName,
		"field_type":                        "text",
		"applies_to_customer_organisations": true,
	})
	fieldKey := strconv.Itoa(fieldID)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/customer-organisations", map[string]interface{}{
		"name": "CFV Org " + ts(),
	})
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	orgID := ExtractIDFromResponse(t, created)

	updateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/customer-organisations/%d", orgID), map[string]interface{}{
		"name":                created["name"],
		"custom_field_values": map[string]interface{}{fieldName: "acme"},
	})
	AssertStatusCode(t, updateResp, http.StatusOK)
	var updated map[string]interface{}
	DecodeJSON(t, updateResp, &updated)

	updatedCFV, _ := updated["custom_field_values"].(map[string]interface{})
	if updatedCFV[fieldKey] != "acme" {
		t.Fatalf("response cfv[%s] = %v, want acme (cfv=%v)", fieldKey, updatedCFV[fieldKey], updatedCFV)
	}
	if _, ok := updatedCFV[fieldName]; ok {
		t.Fatalf("response must strip the unique-name mirror, got %v", updatedCFV)
	}

	stored := readStoredCFV(t, server, "customer_organisations", orgID)
	if stored[fieldKey] != "acme" || stored[fieldName] != "acme" {
		t.Fatalf("stored cfv = %v, want id key %s and name mirror %q", stored, fieldKey, fieldName)
	}
}

func createCustomerCustomField(t *testing.T, server *TestServer, data map[string]interface{}) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/custom-fields", data)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}

func readStoredCFV(t *testing.T, server *TestServer, table string, id int) map[string]interface{} {
	t.Helper()
	var raw string
	//nolint:gosec // table is a test-controlled literal
	if err := server.DB().QueryRow(fmt.Sprintf(`SELECT custom_field_values FROM %s WHERE id = ?`, table), id).Scan(&raw); err != nil {
		t.Fatalf("read %s %d cfv: %v", table, id, err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %s %d cfv %q: %v", table, id, raw, err)
	}
	return out
}
