//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/llm"
)

// failingChatClient returns a provider-style rejection for every completion.
type failingChatClient struct {
	err error
}

func (c failingChatClient) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, c.err
}

func (failingChatClient) Health(context.Context) error { return nil }
func (failingChatClient) Available() bool              { return true }

const providerRejection = "temperature is deprecated for this model"

// grantChatAdmin makes the user a real system admin through the permission
// service, which is what the handler consults (the session user's cached flag
// is not reliable).
func grantChatAdmin(t *testing.T, handler *AIHandler, db database.Database, userID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO user_global_permissions(user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = 'system.admin'
	`, userID); err != nil {
		t.Fatal(err)
	}
	if err := handler.permService.InvalidateUserCache(userID); err != nil {
		t.Fatal(err)
	}
}

// The provider's rejection must be persisted on the failed run and returned to
// system admins so a rejected chat is diagnosable without a proxy capture.
func TestAIChatPersistsProviderErrorAndReturnsItToAdmins(t *testing.T) {
	handler, user, db, _ := newConversationHandler(t)
	handler.chatLLMs = fixedChatResolver{client: failingChatClient{err: errors.New(providerRejection)}}
	grantChatAdmin(t, handler, db, user.ID)

	recorder := chatRequest(t, handler, user, `{"message":"why did this fail","context":{"workspace_id":1}}`)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Error   string            `json:"error"`
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != "The AI service could not complete this request." {
		t.Fatalf("error message=%q", body.Error)
	}
	if !strings.Contains(body.Details["provider_error"], providerRejection) {
		t.Fatalf("provider_error=%q want it to contain %q", body.Details["provider_error"], providerRejection)
	}

	var stored string
	if err := db.QueryRow(
		`SELECT error FROM agent_runs WHERE status='failed' ORDER BY id DESC LIMIT 1`,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, providerRejection) {
		t.Fatalf("persisted agent_runs.error=%q want it to contain %q", stored, providerRejection)
	}
}

// A non-admin must not receive the raw provider message, but the run still
// records it for admins inspecting history.
func TestAIChatHidesProviderErrorFromNonAdmins(t *testing.T) {
	handler, user, db, _ := newConversationHandler(t)
	handler.chatLLMs = fixedChatResolver{client: failingChatClient{err: errors.New(providerRejection)}}

	recorder := chatRequest(t, handler, user, `{"message":"why did this fail","context":{"workspace_id":1}}`)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if _, ok := body.Details["provider_error"]; ok {
		t.Fatalf("non-admin received provider_error: %v", body.Details)
	}

	var stored string
	if err := db.QueryRow(
		`SELECT error FROM agent_runs WHERE status='failed' ORDER BY id DESC LIMIT 1`,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, providerRejection) {
		t.Fatalf("persisted agent_runs.error=%q want it to contain %q", stored, providerRejection)
	}
}

// Credential-shaped text in a provider error must be scrubbed before it is
// logged, returned, or persisted.
func TestAIChatRedactsProviderError(t *testing.T) {
	handler, user, db, _ := newConversationHandler(t)
	handler.chatLLMs = fixedChatResolver{
		client: failingChatClient{err: errors.New("upstream rejected https://user:secret@example.test/v1 with Authorization: Bearer sk-live-1234567890")},
	}
	grantChatAdmin(t, handler, db, user.ID)

	recorder := chatRequest(t, handler, user, `{"message":"why did this fail","context":{"workspace_id":1}}`)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "sk-live-1234567890") {
		t.Fatalf("credential leaked in response: %s", recorder.Body.String())
	}

	var stored string
	if err := db.QueryRow(
		`SELECT error FROM agent_runs WHERE status='failed' ORDER BY id DESC LIMIT 1`,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "secret") || strings.Contains(stored, "sk-live-1234567890") {
		t.Fatalf("credential leaked in persisted run error: %s", stored)
	}
}
