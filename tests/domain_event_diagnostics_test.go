package tests

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"windshift/internal/events"
	"windshift/internal/restapi"
)

func TestDomainEventDiagnosticsAuthorizationIsolationAndReplay(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	_, username, password := CreateTestUserWithCredentials(
		t,
		server,
		"domain_event_diagnostics_user",
		"domain-event-diagnostics-user@test.com",
	)
	nonAdminToken := CreateBearerTokenForUser(t, server, username, password)
	store := events.NewStore(server.DB())
	ctx := context.Background()
	if err := store.ConfigureConsumer(ctx, events.Consumer{
		Key: "test.diagnostics", HandlerVersion: 1, Active: true,
		StartEventID: 1, EventTypes: []string{"test.changed"},
	}); err != nil {
		t.Fatalf("ConfigureConsumer() error = %v", err)
	}
	workspaceOne := 7001
	workspaceTwo := 7002
	first := appendDiagnosticEvent(t, store, workspaceOne, "one")
	appendDiagnosticEvent(t, store, workspaceTwo, "two")
	if created, err := store.Reconcile(ctx, 100); err != nil || created < 2 {
		t.Fatalf("Reconcile() created %d, error = %v", created, err)
	}
	now := time.Now().UTC().Add(time.Second)
	delivery, err := store.Claim(ctx, "test.diagnostics", "test-worker", now, time.Minute)
	if err != nil || delivery == nil {
		t.Fatalf("Claim() delivery = %+v, error = %v", delivery, err)
	}
	if err := store.Fail(ctx, *delivery, errors.New("repair me"), false, now); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}

	path := fmt.Sprintf("/admin/diagnostics/domain-events?workspace_id=%d&consumer_key=test.diagnostics", workspaceOne)
	denied := MakeAuthRequestWithToken(t, server, nonAdminToken, http.MethodGet, path, nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusForbidden)
	var deniedError restapi.ErrorResponse
	DecodeJSON(t, denied, &deniedError)
	if deniedError.Code != restapi.ErrCodeInsufficientPermission {
		t.Fatalf("non-admin diagnostics error = %+v", deniedError)
	}

	response := MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, path, nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var snapshot struct {
		Filter struct {
			WorkspaceID int `json:"workspace_id"`
		} `json:"filter"`
		Consumers []struct {
			ConsumerKey       string `json:"consumer_key"`
			TerminalFailures  int64  `json:"terminal_failures"`
			BlockedAggregates int64  `json:"blocked_aggregates"`
		} `json:"consumers"`
		Failures []struct {
			EventID     int64  `json:"event_id"`
			WorkspaceID int    `json:"workspace_id"`
			EventKey    string `json:"event_key"`
			LastError   string `json:"last_error"`
		} `json:"failures"`
	}
	DecodeJSON(t, response, &snapshot)
	if snapshot.Filter.WorkspaceID != workspaceOne || len(snapshot.Consumers) != 1 ||
		snapshot.Consumers[0].ConsumerKey != "test.diagnostics" ||
		snapshot.Consumers[0].TerminalFailures != 1 || snapshot.Consumers[0].BlockedAggregates != 1 {
		t.Fatalf("diagnostics snapshot = %+v", snapshot)
	}
	if len(snapshot.Failures) != 1 || snapshot.Failures[0].EventID != first.ID ||
		snapshot.Failures[0].WorkspaceID != workspaceOne || snapshot.Failures[0].EventKey != first.Key ||
		snapshot.Failures[0].LastError != "repair me" {
		t.Fatalf("filtered failures = %+v", snapshot.Failures)
	}

	replayPath := fmt.Sprintf("/admin/diagnostics/domain-events/%d/test.diagnostics/replay", first.ID)
	deniedReplay := MakeAuthRequestWithToken(t, server, nonAdminToken, http.MethodPost, replayPath, map[string]any{"reason": "not allowed"})
	defer deniedReplay.Body.Close()
	AssertStatusCode(t, deniedReplay, http.StatusForbidden)

	replay := MakeAuthRequestWithToken(t, server, adminToken, http.MethodPost, replayPath, map[string]any{"reason": "configuration repaired"})
	defer replay.Body.Close()
	AssertStatusCode(t, replay, http.StatusOK)
	var replayResult struct {
		Action         string          `json:"action"`
		Reason         string          `json:"reason"`
		OrderingImpact string          `json:"ordering_impact"`
		Operator       events.Operator `json:"operator"`
	}
	DecodeJSON(t, replay, &replayResult)
	if replayResult.Action != "replay" || replayResult.Reason != "configuration repaired" ||
		replayResult.Operator.Kind != "user" || replayResult.Operator.Ref == "" || replayResult.OrderingImpact == "" {
		t.Fatalf("replay response = %+v", replayResult)
	}
}

func appendDiagnosticEvent(t *testing.T, store *events.Store, workspaceID int, aggregateID string) *events.Event {
	t.Helper()
	event, err := store.AppendStandalone(context.Background(), events.NewEvent{
		WorkspaceID: &workspaceID, AggregateType: "test", AggregateID: aggregateID,
		Type: "test.changed", PayloadVersion: 1, ActorKind: "system",
		SourceKind: "test", Payload: []byte(`{"value":1}`),
	})
	if err != nil {
		t.Fatalf("AppendStandalone() error = %v", err)
	}
	return event
}
