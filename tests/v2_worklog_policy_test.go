package tests

import (
	"fmt"
	"reflect"
	"testing"
)

func worklogPolicyRequest(t *testing.T, s *TestServer, cookie, method, path string, body any, status int, out any) {
	t.Helper()
	url := s.BaseURL + "/api/v2" + path
	response := makeSessionRequest(t, method, url, cookie, body, canonicalJSONHeaders(method, url))
	defer response.Body.Close()
	if response.StatusCode != status {
		AssertStatusCode(t, response, status)
		t.FailNow()
	}
	if out != nil {
		DecodeJSON(t, response, out)
	}
}

func TestV2TimeProjectCreatorIsManager(t *testing.T) {
	s, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, s)
	id, name, password := CreateTestUserWithCredentials(t, s, "creator-policy", "creator-policy@example.com")
	GrantGlobalPermission(t, s, id, "project.manage")
	cookie := CreateBearerTokenForUser(t, s, name, password)
	projectID := createTimeProjectFixture(t, s, "Creator policy source")
	var source struct {
		Data struct {
			CustomerID int `json:"customer_id"`
		} `json:"data"`
	}
	worklogPolicyRequest(t, s, s.SessionCookie, "GET", fmt.Sprintf("/time/projects/%d", projectID), nil, 200, &source)
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	worklogPolicyRequest(t, s, cookie, "POST", "/time/projects", map[string]any{"name": "Creator policy", "customer_id": source.Data.CustomerID}, 201, &created)
	var managers struct {
		Data []struct {
			PrincipalID   int    `json:"principal_id"`
			PrincipalType string `json:"principal_type"`
		} `json:"data"`
	}
	worklogPolicyRequest(t, s, cookie, "GET", fmt.Sprintf("/time/projects/%d/managers", created.Data.ID), nil, 200, &managers)
	if len(managers.Data) != 1 || managers.Data[0].PrincipalID != id || managers.Data[0].PrincipalType != "user" {
		t.Fatalf("creator manager assignment = %+v, want user %d", managers.Data, id)
	}

	RevokeGlobalPermission(t, s, id, GetPermissions(t, s)["project.manage"])
	worklogPolicyRequest(t, s, cookie, "PATCH", fmt.Sprintf("/time/projects/%d", created.Data.ID), map[string]any{"name": "Creator still manages"}, 200, nil)
	var updated struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	worklogPolicyRequest(t, s, cookie, "GET", fmt.Sprintf("/time/projects/%d", created.Data.ID), nil, 200, &updated)
	if updated.Data.Name != "Creator still manages" {
		t.Fatalf("creator management did not persist: %+v", updated.Data)
	}
}

func TestV2WorklogPolicy(t *testing.T) {
	s, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, s)
	viewerID, username, password := CreateTestUserWithCredentials(t, s, "policy-viewer", "policy-viewer@example.com")
	viewer := CreateBearerTokenForUser(t, s, username, password)
	managerID, managerName, managerPassword := CreateTestUserWithCredentials(t, s, "policy-manager", "policy-manager@example.com")
	manager := CreateBearerTokenForUser(t, s, managerName, managerPassword)
	workspaceID, _ := CreateTestWorkspace(t, s, "Policy workspace", "WLP")
	viewerBearer := createTokenWithScopesAsUser(t, s, username, password, []string{"time:read"})
	globalID, globalName, globalPassword := CreateTestUserWithCredentials(t, s, "policy-global", "policy-global@example.com")
	GrantGlobalPermission(t, s, globalID, "project.manage")
	global := CreateBearerTokenForUser(t, s, globalName, globalPassword)
	groupFor := func(name string, userID int) int {
		var group struct {
			Data struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		worklogPolicyRequest(t, s, s.SessionCookie, "POST", "/admin/groups", map[string]any{"name": name}, 201, &group)
		worklogPolicyRequest(t, s, s.SessionCookie, "PATCH", fmt.Sprintf("/admin/groups/%d", group.Data.ID), map[string]any{"member_ids": []int{userID}}, 200, nil)
		return group.Data.ID
	}
	viewerGroup := groupFor("Policy members", viewerID)
	managerGroup := groupFor("Policy managers", managerID)
	for _, tc := range []struct {
		name                        string
		managers, members, assigned bool
		group                       bool
	}{
		{"open", false, false, false, false}, {"managers_only", true, false, false, false}, {"members_only_outsider", false, true, false, false}, {"members_only_member", false, true, true, false}, {"private_member", true, true, true, false}, {"private_outsider", true, true, false, false}, {"group_member_manager", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid := createTimeProjectFixture(t, s, "Policy "+tc.name)
			itemID := CreateTestItem(t, s, workspaceID, "Policy "+tc.name)
			base := fmt.Sprintf("/time/projects/%d", pid)
			var assignments struct {
				Data []struct {
					ID int `json:"id"`
				} `json:"data"`
			}
			worklogPolicyRequest(t, s, s.SessionCookie, "GET", base+"/managers", nil, 200, &assignments)
			for _, a := range assignments.Data {
				worklogPolicyRequest(t, s, s.SessionCookie, "DELETE", fmt.Sprintf("%s/managers/%d", base, a.ID), nil, 204, nil)
			}
			create := func(cookie, description string) int {
				var result struct {
					Data struct {
						ID int `json:"id"`
					} `json:"data"`
				}
				worklogPolicyRequest(t, s, cookie, "POST", "/time/worklogs", map[string]any{"project_id": pid, "item_id": itemID, "description": description, "date": "2026-09-08", "duration": "30m"}, 201, &result)
				return result.Data.ID
			}
			ownID := create(viewer, "Own entry")
			otherID := create(s.SessionCookie, "Other entry")
			if tc.managers {
				principalType, principalID := "user", managerID
				if tc.group {
					principalType, principalID = "group", managerGroup
				}
				worklogPolicyRequest(t, s, s.SessionCookie, "POST", base+"/managers", map[string]any{"principal_type": principalType, "principal_id": principalID}, 201, nil)
			}
			if tc.members {
				memberID := managerID
				if tc.assigned {
					memberID = viewerID
				}
				principalType := "user"
				if tc.group {
					principalType, memberID = "group", viewerGroup
				}
				worklogPolicyRequest(t, s, s.SessionCookie, "POST", base+"/members", map[string]any{"principal_type": principalType, "principal_id": memberID}, 201, nil)
			}
			visible := !tc.members || tc.assigned
			for _, path := range []string{fmt.Sprintf("/time/worklogs?project_id=%d", pid), base + "/worklogs", fmt.Sprintf("/items/%d/worklogs", itemID)} {
				var page struct {
					Data []struct {
						ID        int `json:"id"`
						ProjectID int `json:"project_id"`
					} `json:"data"`
					Pagination struct {
						TotalItems int `json:"total_items"`
					} `json:"pagination"`
				}
				status := 200
				if !visible && path == base+"/worklogs" {
					status = 404
				}
				worklogPolicyRequest(t, s, viewer, "GET", path, nil, status, &page)
				got := []int{}
				for _, row := range page.Data {
					if row.ProjectID == pid {
						got = append(got, row.ID)
					}
				}
				want := []int{}
				if visible {
					want = append(want, ownID)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s worklogs = %v, want %v", path, got, want)
				}
				if page.Pagination.TotalItems != len(want) {
					t.Errorf("%s total=%d want %d", path, page.Pagination.TotalItems, len(want))
				}
			}
			for _, wid := range []int{ownID, otherID} {
				status := 404
				if visible && wid == ownID {
					status = 200
				}
				var result struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				worklogPolicyRequest(t, s, viewer, "GET", fmt.Sprintf("/time/worklogs/%d", wid), nil, status, &result)
				if status == 404 && (result.Error.Code != "not_found" || result.Error.Message != "Worklog was not found") {
					t.Errorf("denial = %+v", result.Error)
				}
			}
			if tc.managers {
				var page struct {
					Data []struct {
						ID int `json:"id"`
					} `json:"data"`
				}
				worklogPolicyRequest(t, s, manager, "GET", base+"/worklogs", nil, 200, &page)
				got := []int{}
				for _, row := range page.Data {
					got = append(got, row.ID)
				}
				if !reflect.DeepEqual(got, []int{otherID, ownID}) {
					t.Errorf("manager rows=%v", got)
				}
			}
			for _, reader := range []string{global, s.SessionCookie} {
				var page struct {
					Data []struct {
						ID int `json:"id"`
					} `json:"data"`
				}
				worklogPolicyRequest(t, s, reader, "GET", base+"/worklogs", nil, 200, &page)
				if len(page.Data) != 2 || page.Data[0].ID != otherID || page.Data[1].ID != ownID {
					t.Errorf("global reader rows=%+v", page.Data)
				}
			}
			bearerResponse := MakeBearerRequestWithToken(t, s, viewerBearer, "GET", "/rest/api/v2"+fmt.Sprintf("/time/worklogs?project_id=%d&page_size=1", pid), nil)
			var bearerPage struct {
				Data []struct {
					ID int `json:"id"`
				} `json:"data"`
				Pagination struct {
					TotalItems int `json:"total_items"`
				} `json:"pagination"`
			}
			AssertStatusCode(t, bearerResponse, 200)
			DecodeJSON(t, bearerResponse, &bearerPage)
			bearerResponse.Body.Close()
			expectedCount := 0
			if visible {
				expectedCount = 1
			}
			if len(bearerPage.Data) != expectedCount || bearerPage.Pagination.TotalItems != expectedCount {
				t.Errorf("bearer page=%+v", bearerPage)
			}
			if visible && len(bearerPage.Data) == 1 && bearerPage.Data[0].ID != ownID {
				t.Errorf("bearer own entry=%+v", bearerPage.Data)
			}

			for _, method := range []string{"PATCH", "DELETE"} {
				var denial struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				var body any
				if method == "PATCH" {
					body = map[string]any{"description": "Unauthorized edit"}
				}
				worklogPolicyRequest(t, s, viewer, method, fmt.Sprintf("/time/worklogs/%d", otherID), body, 404, &denial)
				if denial.Error.Code != "not_found" || denial.Error.Message != "Worklog was not found" {
					t.Errorf("other-owner %s denial=%+v", method, denial)
				}
			}
			var unchanged struct {
				Data struct {
					Description string `json:"description"`
				} `json:"data"`
			}
			worklogPolicyRequest(t, s, s.SessionCookie, "GET", fmt.Sprintf("/time/worklogs/%d", otherID), nil, 200, &unchanged)
			if unchanged.Data.Description != "Other entry" {
				t.Fatalf("unauthorized mutation changed entry: %+v", unchanged.Data)
			}
			var managementDenial struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			worklogPolicyRequest(t, s, viewer, "PATCH", base, map[string]any{"name": "Unauthorized management"}, 403, &managementDenial)
			if managementDenial.Error.Code != "forbidden" || managementDenial.Error.Message != "Time project operation is not permitted" {
				t.Errorf("management denial=%+v", managementDenial)
			}
			worklogPolicyRequest(t, s, global, "PATCH", base, map[string]any{"name": "Global management " + tc.name}, 200, nil)

			if !visible {
				for _, method := range []string{"PATCH", "DELETE"} {
					var denial struct {
						Error struct {
							Code    string `json:"code"`
							Message string `json:"message"`
						} `json:"error"`
					}
					var body any
					if method == "PATCH" {
						body = map[string]any{"description": "forbidden"}
					}
					worklogPolicyRequest(t, s, viewer, method, fmt.Sprintf("/time/worklogs/%d", ownID), body, 404, &denial)
					if denial.Error.Code != "not_found" || denial.Error.Message != "Worklog was not found" {
						t.Errorf("%s denial=%+v", method, denial)
					}
				}
			}
		})
	}
}

func TestV2WorklogRevocationDeniesMovesAndLastMemberRemovalReopens(t *testing.T) {
	s, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, s)
	viewerID, name, password := CreateTestUserWithCredentials(t, s, "revoked-owner", "revoked-owner@example.com")
	viewer := CreateBearerTokenForUser(t, s, name, password)
	source := createTimeProjectFixture(t, s, "Revocation source")
	target := createTimeProjectFixture(t, s, "Revocation target")
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	worklogPolicyRequest(t, s, viewer, "POST", "/time/worklogs", map[string]any{"project_id": source, "description": "Retained private entry", "date": "2026-09-08", "duration": "30m"}, 201, &created)
	path := fmt.Sprintf("/time/worklogs/%d", created.Data.ID)
	otherID, _, _ := CreateTestUserWithCredentials(t, s, "private-member", "private-member@example.com")
	var member struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	worklogPolicyRequest(t, s, s.SessionCookie, "POST", fmt.Sprintf("/time/projects/%d/members", source), map[string]any{"principal_type": "user", "principal_id": otherID}, 201, &member)
	for _, tc := range []struct {
		method, path, message string
		body                  any
	}{
		{"PATCH", path, "Worklog was not found", map[string]any{"project_id": target}},
		{"DELETE", path, "Worklog was not found", nil},
		{"POST", "/time/worklogs", "Time project was not found", map[string]any{"project_id": source, "description": "Forbidden creation", "date": "2026-09-08", "duration": "30m"}},
	} {
		var denial struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		worklogPolicyRequest(t, s, viewer, tc.method, tc.path, tc.body, 404, &denial)
		if denial.Error.Code != "not_found" || denial.Error.Message != tc.message {
			t.Errorf("%s denial=%+v", tc.method, denial)
		}
	}
	var persisted struct {
		Data struct {
			ProjectID   int    `json:"project_id"`
			UserID      int    `json:"user_id"`
			Description string `json:"description"`
		} `json:"data"`
	}
	worklogPolicyRequest(t, s, s.SessionCookie, "GET", path, nil, 200, &persisted)
	if persisted.Data.ProjectID != source || persisted.Data.UserID != viewerID || persisted.Data.Description != "Retained private entry" {
		t.Fatalf("denied mutations changed worklog: %+v", persisted.Data)
	}
	worklogPolicyRequest(t, s, s.SessionCookie, "DELETE", fmt.Sprintf("/time/projects/%d/members/%d", source, member.Data.ID), nil, 204, nil)
	worklogPolicyRequest(t, s, viewer, "GET", path, nil, 200, &persisted)
	worklogPolicyRequest(t, s, viewer, "PATCH", path, map[string]any{"description": "Owner update after reopening"}, 200, nil)
	worklogPolicyRequest(t, s, viewer, "GET", path, nil, 200, &persisted)
	if persisted.Data.Description != "Owner update after reopening" {
		t.Fatalf("owner update not persisted: %+v", persisted.Data)
	}
	worklogPolicyRequest(t, s, viewer, "DELETE", path, nil, 204, nil)
	worklogPolicyRequest(t, s, s.SessionCookie, "GET", path, nil, 404, nil)
}
