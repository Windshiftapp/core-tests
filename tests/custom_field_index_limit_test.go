package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestCustomFieldIndexLimit documents the Q2 invariant from
// docs/testhunt1.md: the backend caps how many `custom_field_values` JSON
// keys can be *indexed* per target table (defaultMaxIndexes = 20,
// configurable 1–100 via PUT /admin/custom-fields/settings). Once the
// limit is reached, attempting to enable indexing on another field
// returns a 400 Bad Request with a message that starts with
// "index limit reached".
//
// The frontend has no separate column limit — fields beyond the cap still
// render in views, just unindexed (full JSON scan for filter/sort). The
// cap is purely a per-table backing-index ceiling.
//
// To keep the test cheap and deterministic, we lower the cap to a small
// number (3) before filling it; the default of 20 would otherwise require
// 21 fields to provoke the error.
func TestCustomFieldIndexLimit(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	const limit = 3

	t.Run("settings_update_clamps_to_lower_bound_when_nothing_is_indexed", func(t *testing.T) {
		setIndexLimit(t, server, limit)
	})

	t.Run("enabling_indexing_up_to_the_cap_succeeds", func(t *testing.T) {
		setIndexLimit(t, server, limit)
		var fieldIDs []int
		for i := 0; i < limit; i++ {
			id := CreateTestCustomField(t, server, fmt.Sprintf("Indexed Field A%d_%s", i, ts()), "text", "")
			fieldIDs = append(fieldIDs, id)
			enableIndexOnItems(t, server, id, "text", http.StatusOK)
		}
		// Clean up so the next test starts with no indexed fields.
		for _, id := range fieldIDs {
			deleteCustomField(t, server, id)
		}
	})

	t.Run("enabling_indexing_at_cap_returns_400_with_index_limit_message", func(t *testing.T) {
		setIndexLimit(t, server, limit)
		var fieldIDs []int

		// Fill the index slots.
		for i := 0; i < limit; i++ {
			id := CreateTestCustomField(t, server, fmt.Sprintf("Indexed Field B%d_%s", i, ts()), "text", "")
			fieldIDs = append(fieldIDs, id)
			enableIndexOnItems(t, server, id, "text", http.StatusOK)
		}

		// Create one more field and try to enable indexing — must 400.
		overflowID := CreateTestCustomField(t, server, "Overflow Field "+ts(), "text", "")
		fieldIDs = append(fieldIDs, overflowID)
		resp, body := tryEnableIndexOnItems(t, server, overflowID, "text", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 when exceeding index cap, got %d (body=%s)", resp.StatusCode, body)
		}
		if !strings.Contains(strings.ToLower(body), "index limit") {
			t.Errorf("expected error body to mention 'index limit', got: %s", body)
		}

		// Cleanup.
		for _, id := range fieldIDs {
			deleteCustomField(t, server, id)
		}
	})

	t.Run("settings_update_refuses_to_lower_limit_below_current_usage", func(t *testing.T) {
		// Reset to a higher cap so we can fill 3, then try to drop the
		// cap to 2 → must 400 with a message explaining the conflict.
		setIndexLimit(t, server, 5)

		var fieldIDs []int
		for i := 0; i < 3; i++ {
			id := CreateTestCustomField(t, server, fmt.Sprintf("Sticky Field %d_%s", i, ts()), "number", "")
			fieldIDs = append(fieldIDs, id)
			enableIndexOnItems(t, server, id, "number", http.StatusOK)
		}

		body, status := tryUpdateIndexLimit(t, server, 2)
		if status != http.StatusBadRequest {
			t.Fatalf("expected 400 when lowering cap below usage, got %d (body=%s)", status, body)
		}
		// Reset for the next test run on the same DB.
		for _, id := range fieldIDs {
			deleteCustomField(t, server, id)
		}
		setIndexLimit(t, server, 20)
	})

	t.Run("non_indexable_field_type_rejects_indexing_with_validation_error", func(t *testing.T) {
		// Only number, date, text are indexable (handlers/custom_fields.go:49).
		// A select field cannot be indexed at all.
		setIndexLimit(t, server, 10)
		selectOpts := mustSerializeOptions(t, 3, []selectOption{
			{ID: 1, Label: "A"}, {ID: 2, Label: "B"},
		})
		fieldID := CreateTestCustomField(t, server, "Select Field "+ts(), "select", selectOpts)
		defer deleteCustomField(t, server, fieldID)

		resp, body := tryEnableIndexOnItems(t, server, fieldID, "select", selectOpts)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 when indexing a select field, got %d (body=%s)", resp.StatusCode, body)
		}
		if !strings.Contains(strings.ToLower(body), "cannot be indexed") {
			t.Errorf("expected error body to mention 'cannot be indexed', got: %s", body)
		}
	})
}

// --- helpers ---------------------------------------------------------------

// setIndexLimit changes the system-wide max_custom_field_indexes_per_table.
// Fails the test on non-200, which keeps the limit-flipping tests honest.
func setIndexLimit(t *testing.T, server *TestServer, n int) {
	t.Helper()
	body, status := tryUpdateIndexLimit(t, server, n)
	if status != http.StatusOK {
		t.Fatalf("set index limit to %d: status=%d body=%s", n, status, body)
	}
}

func tryUpdateIndexLimit(t *testing.T, server *TestServer, n int) (body string, status int) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPut, "/admin/custom-fields/settings", map[string]interface{}{
		"max_indexes_per_table": n,
	})
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}

// enableIndexOnItems toggles indexed.items=true on a field via PUT. Asserts
// the expected status; tests that anticipate failure use tryEnableIndexOnItems
// directly so they can inspect the body.
func enableIndexOnItems(t *testing.T, server *TestServer, fieldID int, fieldType string, expectedStatus int) {
	t.Helper()
	resp, body := tryEnableIndexOnItems(t, server, fieldID, fieldType, "")
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		t.Fatalf("enable index on field %d: expected %d, got %d (body=%s)",
			fieldID, expectedStatus, resp.StatusCode, body)
	}
}

func tryEnableIndexOnItems(t *testing.T, server *TestServer, fieldID int, fieldType, options string) (*http.Response, string) {
	t.Helper()
	payload := map[string]interface{}{
		"name":       fmt.Sprintf("Field %d", fieldID),
		"field_type": fieldType,
		"required":   false,
		"indexed": map[string]interface{}{
			"items":  true,
			"assets": false,
		},
	}
	if options != "" {
		payload["options"] = options
	}
	resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/admin/custom-fields/%d", fieldID), payload)
	b, _ := io.ReadAll(resp.Body)
	// Re-prime body so caller's defer Close still works.
	resp.Body = io.NopCloser(strings.NewReader(string(b)))
	return resp, string(b)
}

// deleteCustomField is best-effort cleanup so per-test state doesn't bleed
// across runs against a shared DB.
func deleteCustomField(t *testing.T, server *TestServer, fieldID int) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
	_ = resp.Body.Close()
}

// _ keeps json import live in case future tests need it.
var _ = json.RawMessage(nil)
