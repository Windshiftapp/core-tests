package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

// The intake permission rule: channelMgmt gates the mailbox, and writes
// additionally require workspace admin on the routing workspace. A mailbox
// manager without workspace admin cannot bind someone else's workspace.
func TestChannelIntakePermissionAndUniqueness(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Intake Workspace", "INTK")
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	channelID := createMailboxChannel(t, server, "Support mailbox")

	intakeBody := map[string]interface{}{
		"folder":       "INBOX",
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	}

	createIntake := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", channelID), intakeBody)
	if createIntake.StatusCode != http.StatusCreated && createIntake.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createIntake.Body)
		createIntake.Body.Close()
		t.Fatalf("create intake: status=%d body=%s", createIntake.StatusCode, body)
	}
	var intake map[string]interface{}
	DecodeJSON(t, createIntake, &intake)
	createIntake.Body.Close()
	intakeID := ExtractIDFromResponse(t, intake)

	// One mailbox backs at most one intake per folder.
	dup := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", channelID), intakeBody)
	if dup.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(dup.Body)
		dup.Body.Close()
		t.Fatalf("duplicate folder: status=%d body=%s, want 409", dup.StatusCode, body)
	}
	dup.Body.Close()

	managerID, managerUsername, managerPassword := CreateTestUserWithCredentials(
		t, server, "intake_manager", "intake-manager@test.com")
	addManager := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", channelID), map[string]interface{}{
		"manager_type": "user",
		"manager_ids":  []int{managerID},
	})
	addManager.Body.Close()

	managerToken := CreateBearerTokenForUser(t, server, managerUsername, managerPassword)

	listResp := MakeAuthRequestWithToken(t, server, managerToken, http.MethodGet, fmt.Sprintf("/channels/%d/intakes", channelID), nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("manager list intakes: status=%d, want 200", listResp.StatusCode)
	}
	listResp.Body.Close()

	forbidden := MakeAuthRequestWithToken(t, server, managerToken, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", channelID), map[string]interface{}{
		"folder":       "Support",
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	})
	if forbidden.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(forbidden.Body)
		forbidden.Body.Close()
		t.Fatalf("non-workspace-admin create: status=%d body=%s, want 403", forbidden.StatusCode, body)
	}
	forbidden.Body.Close()

	update := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/channels/%d/intakes/%d", channelID, intakeID), map[string]interface{}{
		"folder":       "INBOX",
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
		"status":       "disabled",
	})
	if update.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(update.Body)
		update.Body.Close()
		t.Fatalf("update intake: status=%d body=%s", update.StatusCode, body)
	}
	update.Body.Close()

	// Deleting an intake is a write to its workspace, so a mailbox manager
	// without workspace admin must not be able to remove it.
	forbiddenDelete := MakeAuthRequestWithToken(t, server, managerToken, http.MethodDelete, fmt.Sprintf("/channels/%d/intakes/%d", channelID, intakeID), nil)
	if forbiddenDelete.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(forbiddenDelete.Body)
		forbiddenDelete.Body.Close()
		t.Fatalf("non-workspace-admin delete: status=%d body=%s, want 403", forbiddenDelete.StatusCode, body)
	}
	forbiddenDelete.Body.Close()

	del := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/channels/%d/intakes/%d", channelID, intakeID), nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete intake: status=%d, want 204", del.StatusCode)
	}
	del.Body.Close()
}

// createMailboxChannel creates an inbound email channel with connection-only
// config, the shape the WI-1644 intake model uses.
func createMailboxChannel(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      name,
		"type":      "email",
		"direction": "inbound",
		"status":    "disabled",
		"config":    `{"email_auth_method":"basic","imap_host":"imap.example.com","imap_username":"support@example.com","imap_password":"x","imap_port":993,"imap_encryption":"ssl"}`,
	})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create mailbox %s: status=%d body=%s", name, resp.StatusCode, body)
	}
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	resp.Body.Close()
	return ExtractIDFromResponse(t, created)
}

// A portal-linked intake must reference an inbound portal and a workspace that
// portal serves; the routing workspace and item type are stored on the intake.
func TestChannelIntakePortalLinkValidation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	servedWorkspaceID, _ := CreateTestWorkspace(t, server, "Served Workspace", "SRVD")
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Other Workspace", "OTHR")
	_, portalID := SetupPortalChannel(t, server, servedWorkspaceID)
	mailboxID := createMailboxChannel(t, server, "Portal link mailbox")

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Bug")

	// portal_channel_id must reference a portal, not another email channel.
	otherMailboxID := createMailboxChannel(t, server, "Not a portal")
	badPortal := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", mailboxID), map[string]interface{}{
		"folder":            "BadPortal",
		"portal_channel_id": otherMailboxID,
		"workspace_id":      servedWorkspaceID,
		"item_type_id":      itemTypeID,
	})
	badPortal.Body.Close()
	if badPortal.StatusCode != http.StatusBadRequest {
		t.Fatalf("portal_channel_id to email channel: status=%d, want 400", badPortal.StatusCode)
	}

	// The routing workspace must be one the portal serves.
	unserved := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", mailboxID), map[string]interface{}{
		"folder":            "Unserved",
		"portal_channel_id": portalID,
		"workspace_id":      otherWorkspaceID,
		"item_type_id":      itemTypeID,
	})
	unserved.Body.Close()
	if unserved.StatusCode != http.StatusBadRequest {
		t.Fatalf("unserved workspace: status=%d, want 400", unserved.StatusCode)
	}

	ok := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", mailboxID), map[string]interface{}{
		"folder":            "INBOX",
		"portal_channel_id": portalID,
		"workspace_id":      servedWorkspaceID,
		"item_type_id":      itemTypeID,
	})
	if ok.StatusCode != http.StatusCreated && ok.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(ok.Body)
		ok.Body.Close()
		t.Fatalf("valid portal intake: status=%d body=%s", ok.StatusCode, body)
	}
	var intake map[string]interface{}
	DecodeJSON(t, ok, &intake)
	ok.Body.Close()
	if got, _ := intake["portal_channel_id"].(float64); int(got) != portalID {
		t.Errorf("portal_channel_id = %v, want %d", intake["portal_channel_id"], portalID)
	}
	if got, _ := intake["workspace_id"].(float64); int(got) != servedWorkspaceID {
		t.Errorf("workspace_id = %v, want %d", intake["workspace_id"], servedWorkspaceID)
	}
	if got, _ := intake["item_type_id"].(float64); int(got) != itemTypeID {
		t.Errorf("item_type_id = %v, want %d", intake["item_type_id"], itemTypeID)
	}
}

// TestDeletePortalChannelRemovesIntakes covers the orphaned-intake finding:
// deleting a portal must remove the intakes that fed it so the mailbox stops
// polling a target that no longer exists. The delete-impact preview counts them.
func TestDeletePortalChannelRemovesIntakes(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Delete Portal Workspace", "DPRT")
	_, portalID := SetupPortalChannel(t, server, workspaceID)
	mailboxID := createMailboxChannel(t, server, "Retired portal mailbox")

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	createIntake := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", mailboxID), map[string]interface{}{
		"folder":            "INBOX",
		"portal_channel_id": portalID,
		"workspace_id":      workspaceID,
		"item_type_id":      itemTypeID,
	})
	if createIntake.StatusCode != http.StatusCreated && createIntake.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createIntake.Body)
		createIntake.Body.Close()
		t.Fatalf("create portal intake: status=%d body=%s", createIntake.StatusCode, body)
	}
	createIntake.Body.Close()

	impactResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/delete-impact", portalID), nil)
	defer impactResp.Body.Close()
	AssertStatusCode(t, impactResp, http.StatusOK)
	var impact struct {
		IntakeTargets int `json:"intake_targets"`
	}
	DecodeJSON(t, impactResp, &impact)
	if impact.IntakeTargets != 1 {
		t.Fatalf("delete impact intake_targets = %d, want 1", impact.IntakeTargets)
	}

	del := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/channels/%d", portalID), nil)
	defer del.Body.Close()
	AssertStatusCode(t, del, http.StatusNoContent)

	listResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/intakes", mailboxID), nil)
	defer listResp.Body.Close()
	AssertStatusCode(t, listResp, http.StatusOK)
	var intakes []map[string]interface{}
	DecodeJSON(t, listResp, &intakes)
	if len(intakes) != 0 {
		t.Fatalf("intakes after portal delete = %d, want 0", len(intakes))
	}
}

// The intake receipt toggle round-trips through create and update so the admin
// UI can rely on the persisted value.
func TestChannelIntakeSendConfirmationPersists(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Receipt Workspace", "RCPT")
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	channelID := createMailboxChannel(t, server, "Receipt mailbox")

	createResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", channelID), map[string]interface{}{
		"folder":            "INBOX",
		"workspace_id":      workspaceID,
		"item_type_id":      itemTypeID,
		"send_confirmation": true,
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	intakeID := ExtractIDFromResponse(t, created)
	if created["send_confirmation"] != true {
		t.Fatalf("created intake send_confirmation = %v, want true", created["send_confirmation"])
	}

	listResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/intakes", channelID), nil)
	defer listResp.Body.Close()
	AssertStatusCode(t, listResp, http.StatusOK)
	var listed []map[string]interface{}
	DecodeJSON(t, listResp, &listed)
	if len(listed) != 1 || listed[0]["send_confirmation"] != true {
		t.Fatalf("listed intakes = %+v, want one with send_confirmation true", listed)
	}

	updateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/channels/%d/intakes/%d", channelID, intakeID), map[string]interface{}{
		"folder":            "INBOX",
		"workspace_id":      workspaceID,
		"item_type_id":      itemTypeID,
		"send_confirmation": false,
	})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)
	var updated map[string]interface{}
	DecodeJSON(t, updateResp, &updated)
	if updated["send_confirmation"] != false {
		t.Fatalf("updated intake send_confirmation = %v, want false", updated["send_confirmation"])
	}
}

// WI-1714: editing an intake writes to its current target as well as the
// replacement, so the actor must be authorized for both. A mailbox manager who
// administers only the replacement workspace must not divert routing away from
// a workspace they cannot manage.
func TestChannelIntakeUpdateRequiresExistingWorkspaceTarget(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	existingWorkspaceID, _ := CreateTestWorkspace(t, server, "Existing WS", shortKey("EXST"))
	replacementWorkspaceID, _ := CreateTestWorkspace(t, server, "Replacement WS", shortKey("RPLC"))
	// Restrict the existing workspace so the actor has no implicit access there.
	LockDownWorkspace(t, server, existingWorkspaceID)

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	AssociateWorkspaceWithConfigSet(t, server, existingWorkspaceID, configSetID)
	AssociateWorkspaceWithConfigSet(t, server, replacementWorkspaceID, configSetID)

	mailboxID := createMailboxChannel(t, server, "Retarget mailbox")
	intakeID := createWorkspaceIntake(t, server, mailboxID, "INBOX", existingWorkspaceID, itemTypeID)

	// Actor: mailbox manager and administrator of the replacement workspace only.
	actorID, actorUsername, actorPassword := CreateTestUserWithCredentials(t, server, "retarget_actor", "retarget-actor@test.com")
	AssignWorkspaceRole(t, server, actorID, replacementWorkspaceID, "Administrator")
	addManager := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", mailboxID), map[string]interface{}{
		"manager_type": "user",
		"manager_ids":  []int{actorID},
	})
	addManager.Body.Close()
	actorToken := CreateBearerTokenForUser(t, server, actorUsername, actorPassword)

	updateBody := func(workspaceID int, folder string) map[string]interface{} {
		return map[string]interface{}{
			"folder":       folder,
			"workspace_id": workspaceID,
			"item_type_id": itemTypeID,
		}
	}

	// Forbidden: the actor administers the replacement but not the existing
	// workspace, and routing must not move.
	forbidden := MakeAuthRequestWithToken(t, server, actorToken, http.MethodPut,
		fmt.Sprintf("/channels/%d/intakes/%d", mailboxID, intakeID), updateBody(replacementWorkspaceID, "INBOX"))
	if forbidden.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(forbidden.Body)
		forbidden.Body.Close()
		t.Fatalf("retarget with no permission on the existing workspace: status=%d body=%s, want 403", forbidden.StatusCode, body)
	}
	forbidden.Body.Close()
	assertIntakeWorkspace(t, server, mailboxID, intakeID, existingWorkspaceID)

	// Once the actor also administers the existing workspace, a same-target
	// edit and an authorized retarget both succeed.
	AssignWorkspaceRole(t, server, actorID, existingWorkspaceID, "Administrator")

	sameTarget := MakeAuthRequestWithToken(t, server, actorToken, http.MethodPut,
		fmt.Sprintf("/channels/%d/intakes/%d", mailboxID, intakeID), updateBody(existingWorkspaceID, "Support"))
	if sameTarget.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sameTarget.Body)
		sameTarget.Body.Close()
		t.Fatalf("same-target edit: status=%d body=%s, want 200", sameTarget.StatusCode, body)
	}
	sameTarget.Body.Close()
	assertIntakeWorkspace(t, server, mailboxID, intakeID, existingWorkspaceID)

	retarget := MakeAuthRequestWithToken(t, server, actorToken, http.MethodPut,
		fmt.Sprintf("/channels/%d/intakes/%d", mailboxID, intakeID), updateBody(replacementWorkspaceID, "Support"))
	if retarget.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(retarget.Body)
		retarget.Body.Close()
		t.Fatalf("authorized retarget: status=%d body=%s, want 200", retarget.StatusCode, body)
	}
	retarget.Body.Close()
	assertIntakeWorkspace(t, server, mailboxID, intakeID, replacementWorkspaceID)

	// A system admin bypasses the workspace checks: the admin holds only Viewer
	// on the locked-down existing workspace.
	adminRetarget := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/intakes/%d", mailboxID, intakeID), updateBody(existingWorkspaceID, "Admin"))
	if adminRetarget.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(adminRetarget.Body)
		adminRetarget.Body.Close()
		t.Fatalf("system-admin retarget: status=%d body=%s, want 200", adminRetarget.StatusCode, body)
	}
	adminRetarget.Body.Close()
	assertIntakeWorkspace(t, server, mailboxID, intakeID, existingWorkspaceID)
}

// WI-1714: detaching a portal from an intake is a write to the existing portal
// target, so it requires portal management even though the replacement target
// carries no portal.
func TestChannelIntakeUpdateRequiresExistingPortalTarget(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Portal Target WS", shortKey("PTWS"))
	_, portalID := SetupPortalChannel(t, server, workspaceID)

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	mailboxID := createMailboxChannel(t, server, "Portal detach mailbox")
	createIntake := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", mailboxID), map[string]interface{}{
		"folder":            "INBOX",
		"portal_channel_id": portalID,
		"workspace_id":      workspaceID,
		"item_type_id":      itemTypeID,
	})
	if createIntake.StatusCode != http.StatusCreated && createIntake.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createIntake.Body)
		createIntake.Body.Close()
		t.Fatalf("create portal intake: status=%d body=%s", createIntake.StatusCode, body)
	}
	var intake map[string]interface{}
	DecodeJSON(t, createIntake, &intake)
	createIntake.Body.Close()
	intakeID := ExtractIDFromResponse(t, intake)

	// Actor: mailbox manager and workspace admin, but not a portal manager.
	actorID, actorUsername, actorPassword := CreateTestUserWithCredentials(t, server, "detach_actor", "detach-actor@test.com")
	AssignWorkspaceRole(t, server, actorID, workspaceID, "Administrator")
	addManager := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", mailboxID), map[string]interface{}{
		"manager_type": "user",
		"manager_ids":  []int{actorID},
	})
	addManager.Body.Close()
	actorToken := CreateBearerTokenForUser(t, server, actorUsername, actorPassword)

	detachBody := map[string]interface{}{
		"folder":       "INBOX",
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	}

	// Forbidden: the actor cannot manage the existing portal, and the link stays.
	forbidden := MakeAuthRequestWithToken(t, server, actorToken, http.MethodPut,
		fmt.Sprintf("/channels/%d/intakes/%d", mailboxID, intakeID), detachBody)
	if forbidden.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(forbidden.Body)
		forbidden.Body.Close()
		t.Fatalf("portal detach without portal management: status=%d body=%s, want 403", forbidden.StatusCode, body)
	}
	forbidden.Body.Close()
	assertIntakePortal(t, server, mailboxID, intakeID, &portalID)

	// Grant portal management; the detach now succeeds.
	addPortalManager := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", portalID), map[string]interface{}{
		"manager_type": "user",
		"manager_ids":  []int{actorID},
	})
	addPortalManager.Body.Close()

	allowed := MakeAuthRequestWithToken(t, server, actorToken, http.MethodPut,
		fmt.Sprintf("/channels/%d/intakes/%d", mailboxID, intakeID), detachBody)
	if allowed.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(allowed.Body)
		allowed.Body.Close()
		t.Fatalf("authorized portal detach: status=%d body=%s, want 200", allowed.StatusCode, body)
	}
	allowed.Body.Close()
	assertIntakePortal(t, server, mailboxID, intakeID, nil)
}

func createWorkspaceIntake(t *testing.T, server *TestServer, mailboxID int, folder string, workspaceID, itemTypeID int) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", mailboxID), map[string]interface{}{
		"folder":       folder,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create intake: status=%d body=%s", resp.StatusCode, body)
	}
	var intake map[string]interface{}
	DecodeJSON(t, resp, &intake)
	return ExtractIDFromResponse(t, intake)
}

func assertIntakeWorkspace(t *testing.T, server *TestServer, mailboxID, intakeID, wantWorkspaceID int) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/intakes", mailboxID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var intakes []map[string]interface{}
	DecodeJSON(t, resp, &intakes)
	for _, intake := range intakes {
		if int(intake["id"].(float64)) != intakeID {
			continue
		}
		if got := int(intake["workspace_id"].(float64)); got != wantWorkspaceID {
			t.Fatalf("intake %d workspace = %d, want %d (routing must not change)", intakeID, got, wantWorkspaceID)
		}
		return
	}
	t.Fatalf("intake %d not found", intakeID)
}

func assertIntakePortal(t *testing.T, server *TestServer, mailboxID, intakeID int, wantPortalID *int) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/intakes", mailboxID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var intakes []map[string]interface{}
	DecodeJSON(t, resp, &intakes)
	for _, intake := range intakes {
		if int(intake["id"].(float64)) != intakeID {
			continue
		}
		raw, present := intake["portal_channel_id"]
		if wantPortalID == nil {
			if present && raw != nil {
				t.Fatalf("intake %d portal_channel_id = %v, want null", intakeID, raw)
			}
			return
		}
		if !present || raw == nil || int(raw.(float64)) != *wantPortalID {
			t.Fatalf("intake %d portal_channel_id = %v, want %d", intakeID, raw, *wantPortalID)
		}
		return
	}
	t.Fatalf("intake %d not found", intakeID)
}

// The existing-mail import toggle round-trips through create and update so a
// new intake starts from new mail by default and an operator can opt in.
func TestChannelIntakeImportExistingMailPersists(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Import Workspace", "IMPT")
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	channelID := createMailboxChannel(t, server, "Import mailbox")

	createResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", channelID), map[string]interface{}{
		"folder":               "INBOX",
		"workspace_id":         workspaceID,
		"item_type_id":         itemTypeID,
		"import_existing_mail": false,
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	intakeID := ExtractIDFromResponse(t, created)
	if created["import_existing_mail"] != false {
		t.Fatalf("created intake import_existing_mail = %v, want false", created["import_existing_mail"])
	}

	updateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/channels/%d/intakes/%d", channelID, intakeID), map[string]interface{}{
		"folder":               "INBOX",
		"workspace_id":         workspaceID,
		"item_type_id":         itemTypeID,
		"import_existing_mail": true,
	})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)
	var updated map[string]interface{}
	DecodeJSON(t, updateResp, &updated)
	if updated["import_existing_mail"] != true {
		t.Fatalf("updated intake import_existing_mail = %v, want true", updated["import_existing_mail"])
	}

	listResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d/intakes", channelID), nil)
	defer listResp.Body.Close()
	AssertStatusCode(t, listResp, http.StatusOK)
	var listed []map[string]interface{}
	DecodeJSON(t, listResp, &listed)
	if len(listed) != 1 || listed[0]["import_existing_mail"] != true {
		t.Fatalf("listed intakes = %+v, want one with import_existing_mail true", listed)
	}
}
