package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Workspace bundle export/import (WI-1335): round-trip fidelity through the
// production HTTP API — a workspace's pages (tree order, labels, content),
// typed seed items (type, field values, labels), and item links export into
// a versioned bundle and import into a fresh workspace identically modulo
// generated IDs. The embedded configuration-set template travels with the
// bundle and provisions missing item types on the target.

type workspaceBundleDoc struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Source        struct {
		WorkspaceName string `json:"workspace_name"`
	} `json:"source"`
	ConfigurationSet *struct {
		Payload struct {
			ConfigurationSet struct {
				Name string `json:"name"`
			} `json:"configuration_set"`
		} `json:"payload"`
	} `json:"configuration_set_template"`
	Payload struct {
		PageLabels []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"page_labels"`
		Pages []struct {
			Ref       string   `json:"ref"`
			Title     string   `json:"title"`
			ParentRef string   `json:"parent_ref"`
			Content   string   `json:"content"`
			Labels    []string `json:"labels"`
		} `json:"pages"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Items []struct {
			Ref           string         `json:"ref"`
			Title         string         `json:"title"`
			Description   string         `json:"description"`
			ItemTypeName  string         `json:"item_type_name"`
			ParentRef     string         `json:"parent_ref"`
			Labels        []string       `json:"labels"`
			FieldValues   map[string]any `json:"field_values"`
			AssigneeEmail string         `json:"assignee_email"`
		} `json:"items"`
		ItemLinks []struct {
			SourceRef    string `json:"source_ref"`
			TargetRef    string `json:"target_ref"`
			LinkTypeName string `json:"link_type_name"`
		} `json:"item_links"`
	} `json:"payload"`
}

func exportWorkspaceBundle(t *testing.T, server *TestServer, workspaceID int) (workspaceBundleDoc, map[string]any) {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/export", workspaceID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var raw map[string]any
	DecodeJSON(t, resp, &raw)
	rawJSON, _ := json.Marshal(raw)
	var bundle workspaceBundleDoc
	if err := json.Unmarshal(rawJSON, &bundle); err != nil {
		t.Fatalf("decode exported bundle: %v", err)
	}
	if bundle.Kind != "windshift.workspace-bundle" {
		t.Fatalf("export kind = %q", bundle.Kind)
	}
	return bundle, raw
}

func importWorkspaceBundle(t *testing.T, server *TestServer, workspaceID int, bundle map[string]any) (pages, items, links int, outcomes []map[string]any) {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/import", workspaceID), bundle)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data struct {
			PagesImported     int              `json:"pages_imported"`
			ItemsImported     int              `json:"items_imported"`
			ItemLinksImported int              `json:"item_links_imported"`
			Outcomes          []map[string]any `json:"outcomes"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.PagesImported, decoded.Data.ItemsImported, decoded.Data.ItemLinksImported, decoded.Data.Outcomes
}

func TestWorkspaceBundleRoundTrip(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	// -- Source workspace with a restrictive configuration set -------------
	wsA, _ := CreateTestWorkspace(t, server, "Bundle Source WS", "")

	typeResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/item-types", map[string]interface{}{
		"name": "Framework Standard", "hierarchy_level": 0,
	})
	defer typeResp.Body.Close()
	AssertStatusCode(t, typeResp, http.StatusCreated)
	var createdType struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, typeResp, &createdType)
	standardTypeID := createdType.Data.ID

	controlTypeResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/item-types", map[string]interface{}{
		"name": "Framework Control", "hierarchy_level": 1,
	})
	defer controlTypeResp.Body.Close()
	AssertStatusCode(t, controlTypeResp, http.StatusCreated)
	DecodeJSON(t, controlTypeResp, &createdType)
	frameworkTypeID := createdType.Data.ID

	fieldResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
		"name": "Control Score", "field_type": "number",
	})
	defer fieldResp.Body.Close()
	AssertStatusCode(t, fieldResp, http.StatusCreated)
	var createdField struct {
		Data struct {
			CustomField struct {
				ID int `json:"id"`
			} `json:"custom_field"`
		} `json:"data"`
	}
	DecodeJSON(t, fieldResp, &createdField)
	scoreFieldID := createdField.Data.CustomField.ID

	csResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
		"name": "Bundle CS",
		"item_type_configs": []map[string]interface{}{
			{"item_type_id": standardTypeID},
			{"item_type_id": frameworkTypeID},
		},
	})
	defer csResp.Body.Close()
	AssertStatusCode(t, csResp, http.StatusCreated)
	var createdCS struct {
		Data struct {
			ConfigurationSet struct {
				ID int `json:"id"`
			} `json:"configuration_set"`
		} `json:"data"`
	}
	DecodeJSON(t, csResp, &createdCS)
	configSetID := createdCS.Data.ConfigurationSet.ID

	meResp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/users/me", nil)
	defer meResp.Body.Close()
	AssertStatusCode(t, meResp, http.StatusOK)
	var me struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, meResp, &me)
	AssignWorkspaceRole(t, server, me.Data.ID, wsA, "Administrator")

	assign := MakeAuthRequestWithToken(t, server, server.SessionCookie, http.MethodPut,
		fmt.Sprintf("/workspaces/%d/configuration-set", wsA),
		map[string]interface{}{"configuration_set_id": configSetID})
	defer assign.Body.Close()
	AssertStatusCode(t, assign, http.StatusOK)

	// -- Content ------------------------------------------------------------
	pageLabelResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/page-labels", wsA), map[string]interface{}{
		"name": "compliance", "color": "#16a34a",
	})
	defer pageLabelResp.Body.Close()
	AssertStatusCode(t, pageLabelResp, http.StatusCreated)

	labelResp := MakeBearerRequest(t, server, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/labels", wsA), map[string]interface{}{
		"name": "master-list", "color": "#0ea5e9",
	})
	defer labelResp.Body.Close()
	AssertStatusCode(t, labelResp, http.StatusCreated)
	var createdLabel struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, labelResp, &createdLabel)

	rootPage := MakeBearerRequest(t, server, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/pages", wsA), map[string]interface{}{
		"title":   "Control Catalog",
		"content": "Master list of controls.\n\n- AC-1\n- AC-2",
	})
	defer rootPage.Body.Close()
	AssertStatusCode(t, rootPage, http.StatusCreated)
	var createdRoot struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, rootPage, &createdRoot)

	childPage := MakeBearerRequest(t, server, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/pages", wsA), map[string]interface{}{
		"title":     "Access Control",
		"content":   "Access control requirements.",
		"parent_id": createdRoot.Data.ID,
	})
	defer childPage.Body.Close()
	AssertStatusCode(t, childPage, http.StatusCreated)
	var createdChild struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, childPage, &createdChild)

	secondRoot := MakeBearerRequest(t, server, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/pages", wsA), map[string]interface{}{
		"title":   "Clause Library",
		"content": "Clauses A.1 through A.9.",
	})
	defer secondRoot.Body.Close()
	AssertStatusCode(t, secondRoot, http.StatusCreated)

	// Label the pages: compliance on the root, security on the child.
	pageLabelList := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/page-labels", wsA), nil)
	defer pageLabelList.Body.Close()
	var labelCatalog []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	DecodeJSON(t, pageLabelList, &labelCatalog)
	labelByName := map[string]int{}
	for _, l := range labelCatalog {
		labelByName[l.Name] = l.ID
	}
	setRootLabels := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/workspaces/%d/pages/%d/labels", wsA, createdRoot.Data.ID),
		map[string]interface{}{"label_ids": []int{labelByName["compliance"]}})
	defer setRootLabels.Body.Close()
	AssertStatusCode(t, setRootLabels, http.StatusOK)

	itemOne := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/items", map[string]interface{}{
		"workspace_id": wsA,
		"title":        "AC-1 Access control policy",
		"description":  "A policy shall be defined.",
		"item_type_id": standardTypeID,
		"label_ids":    []int{createdLabel.Data.ID},
		"custom_field_values": map[string]interface{}{
			fmt.Sprint(scoreFieldID): 5,
		},
	})
	defer itemOne.Body.Close()
	AssertStatusCode(t, itemOne, http.StatusCreated)
	var createdItemOne struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, itemOne, &createdItemOne)

	itemTwo := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/items", map[string]interface{}{
		"workspace_id": wsA,
		"title":        "AC-2 Review",
		"description":  "The policy shall be reviewed.",
		"item_type_id": frameworkTypeID,
		"parent_id":    createdItemOne.Data.ID,
	})
	defer itemTwo.Body.Close()
	AssertStatusCode(t, itemTwo, http.StatusCreated)
	var createdItemTwo struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, itemTwo, &createdItemTwo)

	// Relate the two items with the seeded Relates To link type.
	linkTypes := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/link-types", nil)
	defer linkTypes.Body.Close()
	var linkTypeDoc struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	DecodeJSON(t, linkTypes, &linkTypeDoc)
	relatesToID := 0
	for _, lt := range linkTypeDoc.Data {
		if lt.Name == "Relates To" {
			relatesToID = lt.ID
		}
	}
	linkResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/links", map[string]interface{}{
		"source_type": "item", "source_id": createdItemOne.Data.ID,
		"target_type": "item", "target_id": createdItemTwo.Data.ID,
		"link_type_id": relatesToID,
	})
	defer linkResp.Body.Close()
	AssertStatusCode(t, linkResp, http.StatusCreated)

	// -- Export --------------------------------------------------------------
	bundle, bundleRaw := exportWorkspaceBundle(t, server, wsA)

	if len(bundle.Payload.Pages) != 3 {
		t.Fatalf("exported pages = %d, want 3", len(bundle.Payload.Pages))
	}
	wantTitles := []string{"Control Catalog", "Access Control", "Clause Library"}
	for i, want := range wantTitles {
		if bundle.Payload.Pages[i].Title != want {
			t.Fatalf("page %d = %q, want %q (tree order must be depth-first)", i, bundle.Payload.Pages[i].Title, want)
		}
	}
	if bundle.Payload.Pages[0].Ref == "" || bundle.Payload.Pages[1].ParentRef != bundle.Payload.Pages[0].Ref {
		t.Fatalf("child page parent_ref must point at the root ref: %+v", bundle.Payload.Pages)
	}
	if bundle.Payload.Pages[0].Content != "Master list of controls.\n\n- AC-1\n- AC-2" {
		t.Fatalf("root content = %q", bundle.Payload.Pages[0].Content)
	}
	if len(bundle.Payload.Pages[0].Labels) != 1 || bundle.Payload.Pages[0].Labels[0] != "compliance" {
		t.Fatalf("root page labels = %+v", bundle.Payload.Pages[0].Labels)
	}
	if len(bundle.Payload.PageLabels) != 1 || bundle.Payload.PageLabels[0].Name != "compliance" {
		t.Fatalf("page label catalog = %+v", bundle.Payload.PageLabels)
	}
	if len(bundle.Payload.Items) != 2 {
		t.Fatalf("exported items = %d, want 2", len(bundle.Payload.Items))
	}
	first := bundle.Payload.Items[0]
	if first.ItemTypeName != "Framework Standard" || first.Labels[0] != "master-list" {
		t.Fatalf("item one = %+v", first)
	}
	score, ok := first.FieldValues["Control Score"].(float64)
	if !ok || score != 5 {
		t.Fatalf("field values must be keyed by custom field name: %+v", first.FieldValues)
	}
	if bundle.Payload.Items[1].ItemTypeName != "Framework Control" {
		t.Fatalf("item two = %+v", bundle.Payload.Items[1])
	}
	if bundle.Payload.Items[1].ParentRef != first.Ref {
		t.Fatalf("item two parent_ref = %q, want %q", bundle.Payload.Items[1].ParentRef, first.Ref)
	}
	if len(bundle.Payload.ItemLinks) != 1 || bundle.Payload.ItemLinks[0].SourceRef != first.Ref ||
		bundle.Payload.ItemLinks[0].TargetRef != bundle.Payload.Items[1].Ref ||
		bundle.Payload.ItemLinks[0].LinkTypeName != "Relates To" {
		t.Fatalf("item links = %+v", bundle.Payload.ItemLinks)
	}
	if bundle.ConfigurationSet == nil || bundle.ConfigurationSet.Payload.ConfigurationSet.Name != "Bundle CS" {
		t.Fatalf("embedded configuration-set template missing: %+v", bundle.ConfigurationSet)
	}
	sourcePayload := bundle.Payload

	// -- Import into a fresh workspace ---------------------------------------
	wsB, _ := CreateTestWorkspace(t, server, "Bundle Target WS", "")
	// Post the raw exported document so the embedded configuration-set
	// template survives the round trip intact.
	importResp := MakeBearerRequest(t, server, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsB), json.RawMessage(mustJSON(bundleRaw)))
	defer importResp.Body.Close()
	AssertStatusCode(t, importResp, http.StatusOK)
	var importResult struct {
		Data struct {
			PagesImported     int `json:"pages_imported"`
			ItemsImported     int `json:"items_imported"`
			ItemLinksImported int `json:"item_links_imported"`
		} `json:"data"`
	}
	DecodeJSON(t, importResp, &importResult)
	if importResult.Data.PagesImported != 3 || importResult.Data.ItemsImported != 2 || importResult.Data.ItemLinksImported != 1 {
		t.Fatalf("import result = %+v", importResult.Data)
	}

	// Round-trip fidelity: re-export the target and compare payloads. The
	// embedded template provisioned "Framework Control" on the target, so the
	// item types resolve without any manual step.
	target, _ := exportWorkspaceBundle(t, server, wsB)
	if target.ConfigurationSet == nil || target.ConfigurationSet.Payload.ConfigurationSet.Name != "Bundle CS" {
		t.Fatalf("target workspace should carry the imported configuration set")
	}
	sourceJSON, _ := json.Marshal(sourcePayload)
	targetJSON, _ := json.Marshal(target.Payload)
	if string(sourceJSON) != string(targetJSON) {
		t.Fatalf("round trip changed the payload:\nsource: %s\ntarget: %s", sourceJSON, targetJSON)
	}
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

func TestWorkspaceBundleImportRefusesUnresolvedReferences(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	wsID, _ := CreateTestWorkspace(t, server, "Refusal Target WS", "")

	bundle := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.workspace-bundle",
		"payload": map[string]interface{}{
			"items": []map[string]interface{}{
				{"ref": "item-1", "title": "Ghost typed item", "item_type_name": "Ghost Type",
					"priority_name": "Ghost Priority", "assignee_email": "nobody@test.local"},
			},
			"item_links": []map[string]interface{}{
				{"source_ref": "item-1", "target_ref": "item-1", "link_type_name": "Ghost Link"},
			},
		},
	}
	resp := MakeBearerRequest(t, server, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsID), bundle)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusUnprocessableEntity)

	var decoded struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Kind  string `json:"kind"`
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"details"`
		} `json:"error"`
	}
	DecodeJSON(t, resp, &decoded)
	if decoded.Error.Code != "unresolved_references" {
		t.Fatalf("error code = %q", decoded.Error.Code)
	}
	found := map[string]bool{}
	for _, item := range decoded.Error.Details {
		found[item.Kind+"|"+item.Name] = true
		if item.Kind == "user" {
			found["user"] = true
			if item.Email != "" {
				t.Fatalf("unresolved report echoed an email: %+v", decoded.Error.Details)
			}
		}
	}
	for _, want := range []string{"item_type|Ghost Type", "priority|Ghost Priority", "user", "link_type|Ghost Link"} {
		if !found[want] {
			t.Fatalf("unresolved report missing %q: %+v", want, decoded.Error.Details)
		}
	}
}

// TestWorkspaceBundleImportScopesAssigneesToWorkspaceMembers covers WI-1814: an
// assignee email may resolve only to a user who can view the target workspace,
// and the unresolved report must not echo the email.
func TestWorkspaceBundleImportScopesAssigneesToWorkspaceMembers(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	wsID, _ := CreateTestWorkspace(t, server, "Assignee Scope WS", "")

	memberID, _, _ := CreateTestUserWithCredentials(t, server, "assignee-member", "assignee-member@test.local")
	AssignWorkspaceRole(t, server, memberID, wsID, "Viewer")
	_, _, _ = CreateTestUserWithCredentials(t, server, "assignee-outsider", "assignee-outsider@test.local")

	importWithAssignee := func(email string) *http.Response {
		t.Helper()
		return MakeBearerRequest(t, server, http.MethodPost,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsID),
			map[string]interface{}{
				"schema_version": 1, "kind": "windshift.workspace-bundle",
				"payload": map[string]interface{}{
					"items": []map[string]interface{}{
						{"ref": "item-1", "title": "Assigned item", "item_type_name": "Task", "assignee_email": email},
					},
				},
			})
	}

	outsideResp := importWithAssignee("assignee-outsider@test.local")
	defer outsideResp.Body.Close()
	AssertStatusCode(t, outsideResp, http.StatusUnprocessableEntity)
	var outsideErr struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Kind  string `json:"kind"`
				Email string `json:"email"`
			} `json:"details"`
		} `json:"error"`
	}
	DecodeJSON(t, outsideResp, &outsideErr)
	if outsideErr.Error.Code != "unresolved_references" {
		t.Fatalf("out-of-scope error code = %q", outsideErr.Error.Code)
	}
	for _, detail := range outsideErr.Error.Details {
		if detail.Email != "" {
			t.Fatalf("unresolved report echoed email %q: %+v", detail.Email, outsideErr.Error.Details)
		}
	}

	memberResp := importWithAssignee("assignee-member@test.local")
	defer memberResp.Body.Close()
	AssertStatusCode(t, memberResp, http.StatusOK)

	bundle, _ := exportWorkspaceBundle(t, server, wsID)
	if len(bundle.Payload.Items) != 1 || bundle.Payload.Items[0].AssigneeEmail != "assignee-member@test.local" {
		t.Fatalf("imported assignee = %+v, want assignee-member@test.local", bundle.Payload.Items)
	}
}

func TestWorkspaceBundleImportIsAtomicPerEntity(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	wsID, _ := CreateTestWorkspace(t, server, "Atomicity Target WS", "")

	// A number custom field; "not-a-number" fails field validation, so the
	// item carrying it must fail without leaving its link behind.
	fieldResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
		"name": "Atomic Score", "field_type": "number",
	})
	defer fieldResp.Body.Close()
	AssertStatusCode(t, fieldResp, http.StatusCreated)

	bundle := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.workspace-bundle",
		"payload": map[string]interface{}{
			"items": []map[string]interface{}{
				{"ref": "item-1", "title": "Good item", "item_type_name": "Task"},
				{"ref": "item-2", "title": "Broken item", "item_type_name": "Task",
					"field_values": map[string]interface{}{"Atomic Score": "not-a-number"}},
			},
			"item_links": []map[string]interface{}{
				{"source_ref": "item-2", "target_ref": "item-1", "link_type_name": "Relates To"},
			},
		},
	}
	pages, items, links, outcomes := importWorkspaceBundle(t, server, wsID, bundle)
	if pages != 0 || items != 1 || links != 0 {
		t.Fatalf("import counts pages=%d items=%d links=%d, want 0/1/0", pages, items, links)
	}
	var brokenOutcome, linkOutcome map[string]any
	for _, o := range outcomes {
		if o["entity"] == "item" && o["ref"] == "item-2" {
			brokenOutcome = o
		}
		if o["entity"] == "item_link" {
			linkOutcome = o
		}
	}
	if brokenOutcome == nil || brokenOutcome["status"] != "failed" {
		t.Fatalf("broken item outcome = %+v", brokenOutcome)
	}
	if linkOutcome == nil || linkOutcome["status"] != "failed" {
		t.Fatalf("link of a failed item must fail too: %+v", linkOutcome)
	}
}

// TestWorkspaceBundleChoiceFieldsRoundTripByLabel verifies WI-1530's
// portability contract for select/multiselect values: export writes option
// labels, and import resolves those labels against the target's own option ids
// (which differ per instance) instead of copying raw ids. It also asserts an
// unknown label fails the item with a diagnostic rather than storing a broken
// value.
func TestWorkspaceBundleChoiceFieldsRoundTripByLabel(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	suffix := ts()
	severityField := CreateTestCustomField(t, server, "Bundle Severity "+suffix, "select",
		mustSerializeOptions(t, 10, []selectOption{{ID: 5, Label: "High"}, {ID: 9, Label: "Low"}}))
	tagsField := CreateTestCustomField(t, server, "Bundle Tags "+suffix, "multiselect",
		mustSerializeOptions(t, 10, []selectOption{{ID: 2, Label: "Alpha"}, {ID: 7, Label: "Beta"}}))

	sourceWS, _ := CreateTestWorkspace(t, server, "Choice Source WS "+suffix, shortKey("CHS"))
	createItemWithCFV(t, server, sourceWS, "Choice item "+suffix, map[string]interface{}{
		strconv.Itoa(severityField): 9,
		strconv.Itoa(tagsField):     []int{2, 7},
	})

	bundle, raw := exportWorkspaceBundle(t, server, sourceWS)
	if len(bundle.Payload.Items) != 1 {
		t.Fatalf("exported items = %d, want 1", len(bundle.Payload.Items))
	}
	values := bundle.Payload.Items[0].FieldValues
	if got, _ := values["Bundle Severity "+suffix].(string); got != "Low" {
		t.Fatalf("select exported as %#v, want label %q", values["Bundle Severity "+suffix], "Low")
	}
	labels, ok := values["Bundle Tags "+suffix].([]any)
	if !ok || len(labels) != 2 || labels[0] != "Alpha" || labels[1] != "Beta" {
		t.Fatalf("multiselect exported as %#v, want labels [Alpha Beta]", values["Bundle Tags "+suffix])
	}

	targetWS, _ := CreateTestWorkspace(t, server, "Choice Target WS "+suffix, shortKey("CHT"))
	pages, items, links, outcomes := importWorkspaceBundle(t, server, targetWS, raw)
	if pages != 0 || items != 1 || links != 0 {
		t.Fatalf("import counts pages=%d items=%d links=%d outcomes=%+v, want 0/1/0", pages, items, links, outcomes)
	}
	imported := findWorkspaceItemByTitle(t, server, targetWS, "Choice item "+suffix)
	if imported == nil {
		t.Fatal("imported item not found in target workspace")
	}
	cfv, _ := imported["custom_field_values"].(map[string]interface{})
	if got, _ := cfv[strconv.Itoa(severityField)].(float64); int(got) != 9 {
		t.Fatalf("imported select value = %#v, want option id 9", cfv[strconv.Itoa(severityField)])
	}
	multi, ok := cfv[strconv.Itoa(tagsField)].([]interface{})
	if !ok || len(multi) != 2 {
		t.Fatalf("imported multiselect value = %#v, want two option ids", cfv[strconv.Itoa(tagsField)])
	}
	if a, _ := multi[0].(float64); int(a) != 2 {
		t.Fatalf("imported multiselect ids = %#v, want [2 7]", multi)
	}
	if b, _ := multi[1].(float64); int(b) != 7 {
		t.Fatalf("imported multiselect ids = %#v, want [2 7]", multi)
	}

	t.Run("unknown option label fails the item with a diagnostic", func(t *testing.T) {
		badWS, _ := CreateTestWorkspace(t, server, "Choice Bad WS "+suffix, shortKey("CHB"))
		badBundle := map[string]any{
			"schema_version": 1,
			"kind":           "windshift.workspace-bundle",
			"payload": map[string]any{
				"items": []map[string]any{{
					"ref": "item-1", "title": "Bad choice " + suffix, "item_type_name": "Task",
					"field_values": map[string]any{"Bundle Severity " + suffix: "Critical"},
				}},
			},
		}
		_, badItems, _, badOutcomes := importWorkspaceBundle(t, server, badWS, badBundle)
		if badItems != 0 {
			t.Fatalf("item with unknown option imported, want failure: %+v", badOutcomes)
		}
		var failed map[string]any
		for _, o := range badOutcomes {
			if o["entity"] == "item" {
				failed = o
			}
		}
		if failed == nil || failed["status"] != "failed" {
			t.Fatalf("unknown option outcome = %+v, want failed item", badOutcomes)
		}
		detail, _ := failed["detail"].(string)
		if !strings.Contains(detail, "not in the field's option set") {
			t.Fatalf("failure detail = %q, want option-set diagnostic", detail)
		}
	})
}

// findWorkspaceItemByTitle returns the first item in the workspace whose title
// matches, or nil.
func findWorkspaceItemByTitle(t *testing.T, server *TestServer, workspaceID int, title string) map[string]interface{} {
	t.Helper()
	for _, item := range GetItemsByWorkspace(t, server, workspaceID) {
		if got, _ := item["title"].(string); got == title {
			return item
		}
	}
	return nil
}

func TestWorkspaceBundlePermissionContract(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	wsID, _ := CreateTestWorkspace(t, server, "Permission WS", "")

	editorID, _, editorPass := CreateTestUserWithCredentials(t, server, "bundle-editor", "bundle-editor@test.local")
	_, editorToken := CreateAuthCredentialsForUser(t, server, "bundle-editor", editorPass)
	AssignWorkspaceRole(t, server, editorID, wsID, "Editor")

	viewerID, _, viewerPass := CreateTestUserWithCredentials(t, server, "bundle-viewer", "bundle-viewer@test.local")
	_, viewerToken := CreateAuthCredentialsForUser(t, server, "bundle-viewer", viewerPass)
	AssignWorkspaceRole(t, server, viewerID, wsID, "Viewer")

	_, _, strangerPass := CreateTestUserWithCredentials(t, server, "bundle-stranger", "bundle-stranger@test.local")
	_, strangerToken := CreateAuthCredentialsForUser(t, server, "bundle-stranger", strangerPass)

	// Editor (member with create rights) may import but not export.
	importAllowed := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsID), map[string]interface{}{
			"schema_version": 1, "kind": "windshift.workspace-bundle",
			"payload": map[string]interface{}{
				"pages": []map[string]interface{}{
					{"ref": "page-1", "title": "Editor page"},
				},
			},
		})
	defer importAllowed.Body.Close()
	AssertStatusCode(t, importAllowed, http.StatusOK)

	editorExport := MakeBearerRequestWithToken(t, server, editorToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/export", wsID), nil)
	defer editorExport.Body.Close()
	AssertStatusCode(t, editorExport, http.StatusForbidden)
	var editorExportErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	DecodeJSON(t, editorExport, &editorExportErr)
	if editorExportErr.Error.Code != "insufficient_permission" {
		t.Fatalf("editor export denial code = %q", editorExportErr.Error.Code)
	}

	// Viewer can read but has no create rights: import denied.
	viewerImport := MakeBearerRequestWithToken(t, server, viewerToken, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsID), map[string]interface{}{
			"schema_version": 1, "kind": "windshift.workspace-bundle",
			"payload": map[string]interface{}{},
		})
	defer viewerImport.Body.Close()
	AssertStatusCode(t, viewerImport, http.StatusForbidden)

	// A non-member cannot even see the workspace: export is 404.
	strangerExport := MakeBearerRequestWithToken(t, server, strangerToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/export", wsID), nil)
	defer strangerExport.Body.Close()
	AssertStatusCode(t, strangerExport, http.StatusNotFound)
}

// TestWorkspaceBundleImportConfigSectionsRequireWorkspaceAdmin covers the
// configuration side doors in a bundle: view_settings and slas are workspace
// configuration (the dedicated board-config and SLA routes require workspace
// admin), so a content-only Editor must not be able to write them through the
// import endpoint. Content-only import stays Editor-allowed.
func TestWorkspaceBundleImportConfigSectionsRequireWorkspaceAdmin(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	wsID, _ := CreateTestWorkspace(t, server, "Bundle Config Gate WS", "BCG")

	editorID, _, editorPass := CreateTestUserWithCredentials(t, server, "bundle-config-editor", "bundle-config-editor@test.local")
	_, editorToken := CreateAuthCredentialsForUser(t, server, "bundle-config-editor", editorPass)
	AssignWorkspaceRole(t, server, editorID, wsID, "Editor")

	assertImport := func(name string, bundle map[string]any, wantStatus int) {
		t.Helper()
		resp := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsID), bundle)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, wantStatus)
	}

	t.Run("view_settings_denied", func(t *testing.T) {
		assertImport("view settings", map[string]any{
			"schema_version": 1, "kind": "windshift.workspace-bundle",
			"payload": map[string]any{
				"view_settings": map[string]any{"enabled_views": []string{"board"}},
			},
		}, http.StatusForbidden)
	})

	t.Run("slas_denied", func(t *testing.T) {
		assertImport("slas", map[string]any{
			"schema_version": 1, "kind": "windshift.workspace-bundle",
			"payload": map[string]any{
				"slas": map[string]any{
					"calendars": []map[string]any{{
						"ref": "editor-calendar", "name": "Editor Calendar", "timezone": "UTC",
						"weekly_intervals": map[string]any{}, "holidays": []any{},
					}},
				},
			},
		}, http.StatusForbidden)
	})

	t.Run("content_only_allowed", func(t *testing.T) {
		assertImport("content", map[string]any{
			"schema_version": 1, "kind": "windshift.workspace-bundle",
			"payload": map[string]any{
				"pages": []map[string]any{{"ref": "page-1", "title": "Editor content page"}},
			},
		}, http.StatusOK)
	})
}

// WI-1559: an embedded configuration-set template provisions global
// configuration entities and replaces the workspace's assignment — powers the
// standalone config-set import surface gates behind system administration.
// Content-only import stays available to Editors; a bundle carrying an
// embedded template requires a system administrator and must be rejected
// before any content or configuration is written.
func TestWorkspaceBundleImportEmbeddedConfigSetRequiresAdmin(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Embedded Template WS", "")

	// Give the workspace a prior configuration-set assignment a denial must
	// preserve.
	priorResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
		"name": "Prior Set", "workspace_ids": []int{wsID},
	})
	defer priorResp.Body.Close()
	AssertStatusCode(t, priorResp, http.StatusCreated)
	var priorSet struct {
		Data struct {
			ConfigurationSet struct {
				ID int `json:"id"`
			} `json:"configuration_set"`
		} `json:"data"`
	}
	DecodeJSON(t, priorResp, &priorSet)
	if priorSet.Data.ConfigurationSet.ID == 0 {
		t.Fatalf("prior configuration set not created: %+v", priorSet.Data)
	}

	// An Editor holds the content-import rights (workspaces:write plus page
	// and item create) but no configuration-admin powers.
	editorID, _, editorPass := CreateTestUserWithCredentials(t, server, "bundle-cs-editor", "bundle-cs-editor@test.local")
	editorSession, editorToken := CreateAuthCredentialsForUser(t, server, "bundle-cs-editor", editorPass)
	AssignWorkspaceRole(t, server, editorID, wsID, "Editor")

	bundle := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.workspace-bundle",
		"configuration_set_template": map[string]interface{}{
			"schema_version": 1,
			"kind":           "windshift.configuration-set",
			"payload": map[string]interface{}{
				"configuration_set": map[string]interface{}{"name": "Smuggled Set"},
			},
		},
		"payload": map[string]interface{}{
			"pages": []map[string]interface{}{
				{"ref": "page-1", "title": "Smuggled page"},
			},
		},
	}

	t.Run("bearer_denial", func(t *testing.T) {
		denial := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsID), bundle)
		defer denial.Body.Close()
		AssertStatusCode(t, denial, http.StatusForbidden)
		var denialErr struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		DecodeJSON(t, denial, &denialErr)
		if denialErr.Error.Code != "insufficient_permission" {
			t.Fatalf("denial code = %q", denialErr.Error.Code)
		}
	})

	t.Run("session_denial", func(t *testing.T) {
		denial := MakeAuthRequestWithToken(t, server, editorSession, http.MethodPost,
			fmt.Sprintf("/v2/workspaces/%d/import", wsID), bundle)
		defer denial.Body.Close()
		AssertStatusCode(t, denial, http.StatusForbidden)
		var denialErr struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		DecodeJSON(t, denial, &denialErr)
		if denialErr.Error.Code != "insufficient_permission" {
			t.Fatalf("denial code = %q", denialErr.Error.Code)
		}
	})

	t.Run("denial_writes_nothing", func(t *testing.T) {
		// No global configuration entity was provisioned.
		listResp := MakeBearerRequest(t, server, http.MethodGet,
			"/rest/api/v2/configuration-sets?search=Smuggled", nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)
		var list struct {
			Data []struct {
				Name string `json:"name"`
			} `json:"data"`
		}
		DecodeJSON(t, listResp, &list)
		if len(list.Data) != 0 {
			t.Fatalf("rejected import provisioned global configuration entities: %+v", list.Data)
		}

		// No content was written either: the bundle's page is absent and the
		// workspace still carries its prior configuration-set assignment.
		target, _ := exportWorkspaceBundle(t, server, wsID)
		if len(target.Payload.Pages) != 0 {
			t.Fatalf("rejected import wrote content: %+v", target.Payload.Pages)
		}
		if target.ConfigurationSet == nil ||
			target.ConfigurationSet.Payload.ConfigurationSet.Name != "Prior Set" {
			t.Fatalf("prior configuration assignment not preserved: %+v", target.ConfigurationSet)
		}
	})

	t.Run("authorized_admin_import_succeeds", func(t *testing.T) {
		importResp := MakeBearerRequest(t, server, http.MethodPost,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/import", wsID), bundle)
		defer importResp.Body.Close()
		AssertStatusCode(t, importResp, http.StatusOK)

		listResp := MakeBearerRequest(t, server, http.MethodGet,
			"/rest/api/v2/configuration-sets?search=Smuggled", nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)
		var list struct {
			Data []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		DecodeJSON(t, listResp, &list)
		if len(list.Data) != 1 || list.Data[0].Name != "Smuggled Set" {
			t.Fatalf("admin import did not provision the embedded set: %+v", list.Data)
		}

		target, _ := exportWorkspaceBundle(t, server, wsID)
		if target.ConfigurationSet == nil ||
			target.ConfigurationSet.Payload.ConfigurationSet.Name != "Smuggled Set" {
			t.Fatalf("admin import did not attach the embedded set: %+v", target.ConfigurationSet)
		}
		if len(target.Payload.Pages) != 1 || target.Payload.Pages[0].Title != "Smuggled page" {
			t.Fatalf("admin import content = %+v", target.Payload.Pages)
		}
	})
}

// TestWorkspaceBundleImportAppliesViewSettingsAndSLAs covers the pack-facing
// bundle sections: a nav preset lands on the workspace-scope board
// configuration, and SLA calendars/metrics seed by name. Re-import converges
// instead of duplicating.
func TestWorkspaceBundleImportAppliesViewSettingsAndSLAs(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Bundle Nav SLA WS", shortKey("BNS"))

	bundle := map[string]any{
		"schema_version": 1,
		"kind":           "windshift.workspace-bundle",
		"payload": map[string]any{
			"view_settings": map[string]any{
				"enabled_views": []string{"backlog", "board", "list", "queue"},
			},
			"slas": map[string]any{
				"calendars": []map[string]any{{
					"ref": "business-hours", "name": "Business Hours", "timezone": "UTC",
					"weekly_intervals": map[string]any{
						"monday": []map[string]string{{"start": "09:00", "end": "17:00"}},
					},
					"holidays":   []any{},
					"is_default": true,
				}},
				"metrics": []map[string]any{{
					"name": "First Response", "display_format": "time", "is_active": true,
					"conditions": []map[string]any{
						{"phase": "start", "condition_type": "created"},
						{"phase": "stop", "condition_type": "assignee_set"},
					},
					"goals": []map[string]any{{
						"position": 0, "ql_query": "1 = 1",
						"targets": []map[string]any{
							{"priority_name": "High", "target_ms": 3600000, "calendar_ref": "business-hours"},
							{"is_fallback": true, "target_ms": 7200000, "calendar_ref": "business-hours"},
						},
					}},
				}},
			},
		},
	}

	pages, items, links, _ := importWorkspaceBundle(t, server, wsID, bundle)
	if pages != 0 || items != 0 || links != 0 {
		t.Fatalf("import counts = %d/%d/%d, want 0/0/0", pages, items, links)
	}

	configResp := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/board-configuration", wsID), nil)
	defer configResp.Body.Close()
	AssertStatusCode(t, configResp, http.StatusOK)
	var configDoc struct {
		Data struct {
			ViewSettings struct {
				EnabledViews *[]string `json:"enabled_views"`
			} `json:"view_settings"`
		} `json:"data"`
	}
	DecodeJSON(t, configResp, &configDoc)
	if configDoc.Data.ViewSettings.EnabledViews == nil {
		t.Fatal("view settings were not applied")
	}
	if got := strings.Join(*configDoc.Data.ViewSettings.EnabledViews, ","); got != "backlog,board,list,queue" {
		t.Fatalf("enabled views = %q, want backlog,board,list,queue", got)
	}

	calendars := listBundleSLACalendars(t, server, wsID)
	if len(calendars) != 1 || calendars[0].Name != "Business Hours" {
		t.Fatalf("calendars = %+v", calendars)
	}
	metrics := listBundleSLAMetrics(t, server, wsID)
	if len(metrics) != 1 || metrics[0].Name != "First Response" {
		t.Fatalf("metrics = %+v", metrics)
	}

	// The priority and fallback targets become separate goals.
	goalCount := countBundleSLAGoals(t, server, wsID)
	if goalCount != 2 {
		t.Fatalf("bundle goals = %d, want 2 (priority + fallback)", goalCount)
	}
	var priorityGoals int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM sla_goals g JOIN sla_metrics m ON m.id = g.metric_id
		WHERE m.workspace_id = ? AND g.ql_query LIKE '%priority = ''High''%'`, wsID).Scan(&priorityGoals); err != nil {
		t.Fatalf("count priority goals: %v", err)
	}
	if priorityGoals != 1 {
		t.Fatalf("priority goals = %d, want 1", priorityGoals)
	}

	importWorkspaceBundle(t, server, wsID, bundle)
	if got := listBundleSLACalendars(t, server, wsID); len(got) != 1 {
		t.Fatalf("re-import duplicated calendars: %+v", got)
	}
	if got := listBundleSLAMetrics(t, server, wsID); len(got) != 1 {
		t.Fatalf("re-import duplicated metrics: %+v", got)
	}
	if got := countBundleSLAGoals(t, server, wsID); got != 2 {
		t.Fatalf("re-import changed goal count to %d, want 2", got)
	}
}

func countBundleSLAGoals(t *testing.T, server *TestServer, workspaceID int) int {
	t.Helper()
	var count int
	if err := server.DB().QueryRow(`SELECT COUNT(*) FROM sla_goals g JOIN sla_metrics m ON m.id = g.metric_id WHERE m.workspace_id = ?`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count bundle goals: %v", err)
	}
	return count
}

type bundleSLACalendar struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type bundleSLAMetric struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func listBundleSLACalendars(t *testing.T, server *TestServer, workspaceID int) []bundleSLACalendar {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars", workspaceID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var doc struct {
		Data []bundleSLACalendar `json:"data"`
	}
	DecodeJSON(t, resp, &doc)
	return doc.Data
}

func listBundleSLAMetrics(t *testing.T, server *TestServer, workspaceID int) []bundleSLAMetric {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var doc struct {
		Data []bundleSLAMetric `json:"data"`
	}
	DecodeJSON(t, resp, &doc)
	return doc.Data
}
