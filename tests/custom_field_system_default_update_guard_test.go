package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestSystemDefaultCustomFieldUpdateGuard pins the read-only contract for
// system-default custom fields: the admin update endpoint must refuse them
// with 403, matching the Delete guard. Without this, admins could rewrite
// name/required/options on system data, and option removal would enqueue
// scrubbing of item/asset values.
//
// Fixture exception: the HTTP API cannot create system_default fields (and no
// production path does), so the field is seeded with a direct write to the
// production schema — the only way to reach the guarded state.
func TestSystemDefaultCustomFieldUpdateGuard(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	// Create a normal field first, then flip it to system_default directly.
	fieldID := CreateTestCustomField(t, server, "System Default Guarded "+ts(), "select", `{"next_id":3,"items":[{"id":1,"label":"A"},{"id":2,"label":"B"}]}`)
	if _, err := server.DB().ExecWrite(`UPDATE custom_field_definitions SET system_default = true WHERE id = ?`, fieldID); err != nil {
		t.Fatalf("seed system_default flag: %v", err)
	}

	t.Run("update_is_forbidden", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/admin/custom-fields/%d", fieldID), map[string]interface{}{
			"name":       "Renamed System Field",
			"field_type": "select",
			"required":   true,
			"options":    `{"next_id":2,"items":[{"id":1,"label":"A"}]}`,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	// The field must be untouched after the refused update.
	var name string
	var required, systemDefault bool
	if err := server.DB().QueryRow(`SELECT name, required, system_default FROM custom_field_definitions WHERE id = ?`, fieldID).
		Scan(&name, &required, &systemDefault); err != nil {
		t.Fatalf("read back custom field: %v", err)
	}
	if name == "Renamed System Field" {
		t.Errorf("system-default field name was rewritten via update endpoint")
	}
	if required {
		t.Errorf("system-default field required flag was flipped via update endpoint")
	}
	if !systemDefault {
		t.Errorf("system_default flag lost")
	}
}
