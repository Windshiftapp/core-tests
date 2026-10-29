package tests

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// WI-1824: asset select and multiselect option ids are integers. A fractional
// JSON number must be rejected with 400 instead of being truncated into a
// valid id.
func TestAssetOptionIDsRejectFractionalValues(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	token := adminAssetToken(t, server)
	setID, typeID := seedAssetSetAndType(t, server, "option-id")

	options := mustSerializeOptions(t, 4, []selectOption{
		{ID: 1, Label: "Low"},
		{ID: 2, Label: "High"},
	})
	selectID := CreateTestCustomField(t, server, "Priority "+ts(), "select", options)
	multiID := CreateTestCustomField(t, server, "Tags "+ts(), "multiselect", options)
	// ReplaceTypeFields replaces the whole assignment list, so both fields
	// must be attached in one PUT.
	attach := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/asset-types/%d/fields", typeID), map[string]any{
		"fields": []map[string]any{
			{"custom_field_id": selectID, "is_required": false, "display_order": 0},
			{"custom_field_id": multiID, "is_required": false, "display_order": 1},
		},
	})
	attach.Body.Close()
	AssertStatusCode(t, attach, http.StatusOK)

	base := fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID)
	create := func(t *testing.T, title string, cfv map[string]any) *http.Response {
		t.Helper()
		return MakeBearerRequestWithToken(t, server, token, http.MethodPost, base, map[string]any{
			"title":               title,
			"asset_type_id":       typeID,
			"custom_field_values": cfv,
		})
	}

	t.Run("select fractional option id is rejected", func(t *testing.T) {
		resp := create(t, "Fractional select "+ts(), map[string]any{
			strconv.Itoa(selectID): 1.5,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("multiselect fractional option id is rejected", func(t *testing.T) {
		resp := create(t, "Fractional multiselect "+ts(), map[string]any{
			strconv.Itoa(multiID): []any{1.5},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("integral option id is accepted", func(t *testing.T) {
		resp := create(t, "Integral select "+ts(), map[string]any{
			strconv.Itoa(selectID): 2,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
	})
}
