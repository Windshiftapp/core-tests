//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/contextkeys"
	"windshift/internal/llm"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func newDailyBriefingHandler(t *testing.T) (*AIHandler, *models.User) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var userID int
	if err := db.QueryRow(`
		INSERT INTO users(email, username, first_name, last_name, is_active)
		VALUES ('briefing@example.test', 'briefing-user', 'Briefing', 'User', true) RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	permissions, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })

	handler := NewAIHandler(
		db, llm.NewConnectionManager(db, nil, nil), permissions,
		services.NewTimePermissionService(db, permissions),
		nil, llm.NewPromptStore(""), nil, nil, nil,
	)
	return handler, &models.User{ID: userID, Username: "briefing-user", Timezone: "UTC"}
}

func getDailyBriefing(t *testing.T, handler *AIHandler, user *models.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/ai/daily-briefing", nil)
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, user))
	recorder := httptest.NewRecorder()
	handler.GetDailyBriefing(recorder, req)
	return recorder
}

// TestGetDailyBriefingWithoutStoredBriefingReturnsEmptyAndStaysQuiet pins that
// a missing briefing — the normal state before the scheduler has generated one,
// and always when no AI provider is configured — is answered with an empty body
// and is not logged as a failure.
func TestGetDailyBriefingWithoutStoredBriefingReturnsEmptyAndStaysQuiet(t *testing.T) {
	handler, user := newDailyBriefingHandler(t)

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	recorder := getDailyBriefing(t, handler, user)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if content, _ := body["content"].(string); content != "" {
		t.Fatalf("content = %q, want empty", content)
	}

	logged := buf.String()
	if strings.Contains(logged, "entity not found") || strings.Contains(logged, "no briefing found") {
		t.Fatalf("missing briefing logged as a failure: %s", logged)
	}
}
