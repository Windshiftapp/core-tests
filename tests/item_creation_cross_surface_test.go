package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestItemCreation_CookieAndRESTV1Contract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Item Creation Contract", shortKey("ICC"))

	itemTypes := GetItemTypes(t, server, GetDefaultConfigurationSet(t, server))
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	create := func(surface string) map[string]interface{} {
		t.Helper()
		body := map[string]interface{}{
			"workspace_id": workspaceID,
			"item_type_id": itemTypeID,
			"title":        "  Promise<Anything> shared item\t",
			"description":  "before<script>bad()</script><br/>after",
		}
		var response *http.Response
		if surface == "cookie" {
			response = MakeAuthRequest(t, server, http.MethodPost, "/items", body)
		} else {
			response = MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v1/items", body)
		}
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var item map[string]interface{}
		DecodeJSON(t, response, &item)
		return item
	}

	for _, surface := range []string{"cookie", "v1"} {
		item := create(surface)
		if item["title"] != "Promise<Anything> shared item" || item["description"] != "before<script>bad()</script><br/>after" {
			t.Fatalf("%s create changed source = %v", surface, item)
		}
		rendered, _ := item["description_html"].(string)
		if strings.Contains(rendered, "<script>") || !strings.Contains(rendered, "&lt;script&gt;") || !strings.Contains(rendered, "<br>") {
			t.Fatalf("%s description_html is not safe rendered Markdown: %q", surface, rendered)
		}
	}

	for _, tc := range []struct {
		name string
		body map[string]interface{}
	}{
		{
			name: "title is whitespace only",
			body: map[string]interface{}{
				"workspace_id": workspaceID,
				"item_type_id": itemTypeID,
				"title":        "   ",
			},
		},
		{
			name: "unknown item type",
			body: map[string]interface{}{
				"workspace_id": workspaceID,
				"item_type_id": 999999,
				"title":        "Invalid type",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for surface, request := range map[string]func() *http.Response{
				"cookie": func() *http.Response {
					return MakeAuthRequest(t, server, http.MethodPost, "/items", tc.body)
				},
				"v1": func() *http.Response {
					return MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v1/items", tc.body)
				},
			} {
				response := request()
				if response.StatusCode != http.StatusBadRequest {
					response.Body.Close()
					t.Fatalf("%s create status = %d, want 400; body=%s", surface, response.StatusCode, fmt.Sprint(tc.body))
				}
				response.Body.Close()
			}
		})
	}

	inactiveResponse := MakeAuthRequest(t, server, http.MethodPost, "/users", map[string]interface{}{
		"email":      "inactive-item-assignee@example.test",
		"username":   "inactive-item-assignee",
		"first_name": "Inactive",
		"last_name":  "Assignee",
		"password":   "testpass123",
		"is_active":  false,
	})
	defer inactiveResponse.Body.Close()
	AssertStatusCode(t, inactiveResponse, http.StatusCreated)
	var inactiveUser map[string]interface{}
	DecodeJSON(t, inactiveResponse, &inactiveUser)
	inactiveUserID := ExtractIDFromResponse(t, inactiveUser)

	for _, assignee := range []struct {
		name string
		id   int
	}{
		{name: "inactive", id: inactiveUserID},
		{name: "unknown", id: inactiveUserID + 1_000_000},
	} {
		t.Run(assignee.name+" assignee", func(t *testing.T) {
			body := map[string]interface{}{
				"workspace_id": workspaceID,
				"item_type_id": itemTypeID,
				"title":        "Rejected " + assignee.name + " assignee",
				"assignee_id":  assignee.id,
			}

			for surface, request := range map[string]func() *http.Response{
				"cookie": func() *http.Response {
					return MakeAuthRequest(t, server, http.MethodPost, "/items", body)
				},
				"v1": func() *http.Response {
					return MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v1/items", body)
				},
			} {
				response := request()
				AssertStatusCode(t, response, http.StatusBadRequest)

				var errorBody map[string]interface{}
				DecodeJSON(t, response, &errorBody)
				response.Body.Close()
				if errorBody["code"] != "VALIDATION_FAILED" {
					t.Fatalf("%s error code = %v, want VALIDATION_FAILED", surface, errorBody["code"])
				}

				if surface == "cookie" {
					if errorBody["error"] != "assignee_id: Assignee user not found" {
						t.Fatalf("cookie error = %v, want field-scoped assignee error", errorBody["error"])
					}
					continue
				}
				if errorBody["error"] != "Assignee user not found" {
					t.Fatalf("v1 error = %v, want Assignee user not found", errorBody["error"])
				}
				details, ok := errorBody["details"].(map[string]interface{})
				if !ok || details["field"] != "assignee_id" {
					t.Fatalf("v1 validation details = %v, want field=assignee_id", errorBody["details"])
				}
			}
		})
	}
}

func TestItemCreation_NumberAndDateCustomFieldValidation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Typed Custom Fields", shortKey("TCF"))
	itemTypes := GetItemTypes(t, server, GetDefaultConfigurationSet(t, server))
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	numberFieldID := CreateTestCustomField(t, server, "Estimate "+ts(), "number", "")
	dateFieldID := CreateTestCustomField(t, server, "Target date "+ts(), "date", "")
	numberKey := fmt.Sprintf("%d", numberFieldID)
	dateKey := fmt.Sprintf("%d", dateFieldID)

	request := func(title string, values map[string]interface{}) *http.Response {
		t.Helper()
		body := map[string]interface{}{
			"workspace_id":        workspaceID,
			"item_type_id":        itemTypeID,
			"title":               title,
			"custom_field_values": values,
		}
		return MakeAuthRequest(t, server, http.MethodPost, "/items", body)
	}

	t.Run("rejects invalid number", func(t *testing.T) {
		response := request("Invalid number", map[string]interface{}{numberKey: "abc"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusBadRequest)
		var body map[string]interface{}
		DecodeJSON(t, response, &body)
		if body["code"] != "VALIDATION_FAILED" || !strings.Contains(fmt.Sprint(body), "number value must be numeric") {
			t.Fatalf("invalid-number response = %v", body)
		}
	})

	t.Run("rejects invalid date", func(t *testing.T) {
		response := request("Invalid date", map[string]interface{}{dateKey: "2026-02-30"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusBadRequest)
		var body map[string]interface{}
		DecodeJSON(t, response, &body)
		if body["code"] != "VALIDATION_FAILED" || !strings.Contains(fmt.Sprint(body), "YYYY-MM-DD") {
			t.Fatalf("invalid-date response = %v", body)
		}
	})

	t.Run("normalizes valid values", func(t *testing.T) {
		response := request("Valid typed fields", map[string]interface{}{
			numberKey: "12.5",
			dateKey:   " 2026-02-28 ",
		})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var item map[string]interface{}
		DecodeJSON(t, response, &item)
		values, ok := item["custom_field_values"].(map[string]interface{})
		if !ok {
			t.Fatalf("custom_field_values = %#v", item["custom_field_values"])
		}
		if values[numberKey] != float64(12.5) || values[dateKey] != "2026-02-28" {
			t.Fatalf("normalized custom fields = %#v", values)
		}
	})
}
