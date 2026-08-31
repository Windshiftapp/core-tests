package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
)

type conversationFixture struct {
	repo    *AgentConversationRepository
	runs    *AgentRunRepository
	humanID int
	otherID int
	agentID int
}

func newConversationFixture(t *testing.T) conversationFixture {
	t.Helper()
	db := openAgentRunTestDB(t)
	seedUser := func(email, username string, isAgent bool) int {
		t.Helper()
		res, err := db.Exec(`
			INSERT INTO users(email, username, first_name, last_name, is_agent, is_active)
			VALUES (?, ?, ?, 'User', ?, true)
		`, email, username, username, isAgent)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return int(id)
	}
	return conversationFixture{
		repo:    NewAgentConversationRepository(db),
		runs:    NewAgentRunRepository(db),
		humanID: seedUser("human@example.test", "human", false),
		otherID: seedUser("other@example.test", "other", false),
		agentID: seedUser("agent@example.test", "agent", true),
	}
}

func TestAgentConversationRepositoryGeneralSessionIsUniqueAndParticipantPrivate(t *testing.T) {
	ctx := context.Background()
	fx := newConversationFixture(t)
	first, err := fx.repo.EnsureGeneralSession(ctx, fx.humanID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fx.repo.EnsureGeneralSession(ctx, fx.humanID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.SessionType != models.AgentSessionGeneral {
		t.Fatalf("General session identity drifted: first=%#v second=%#v", first, second)
	}
	if len(first.Participants) != 1 || first.Participants[0].UserID != fx.humanID {
		t.Fatalf("General participants = %#v", first.Participants)
	}
	if _, err := fx.repo.GetForParticipant(ctx, first.ID, fx.otherID); !errors.Is(err, ErrAgentSessionNotFound) {
		t.Fatalf("cross-user session read error = %v, want not found", err)
	}
}

func TestAgentConversationRepositoryBeginTurnAtomicallyPersistsExactBodyRunAndAudit(t *testing.T) {
	ctx := context.Background()
	fx := newConversationFixture(t)
	session, err := fx.repo.EnsureGeneralSession(ctx, fx.humanID)
	if err != nil {
		t.Fatal(err)
	}
	const exactBody = "Keep <b>this exact body</b> & punctuation."
	begun, err := fx.repo.BeginTurn(ctx, BeginAgentTurnInput{
		SessionID:      session.ID,
		SenderUserID:   fx.humanID,
		SenderUsername: "human",
		ActingUserID:   fx.humanID,
		WorkspaceID:    1,
		JobKind:        models.JobKindGeneralAgent,
		Content:        exactBody,
		ContextJSON:    `{"view":"workspace-pages","workspace_id":1}`,
		GrantsJSON:     `{"workspace_ids":[1],"tools":["get_item"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := fx.repo.ListMessagesForParticipant(ctx, session.ID, fx.humanID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != exactBody ||
		messages[0].AgentRunID == nil || *messages[0].AgentRunID != begun.RunID {
		t.Fatalf("persisted user message = %#v", messages)
	}
	run, err := fx.runs.Get(ctx, begun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.JobKind != models.JobKindGeneralAgent || run.SessionID != "1" ||
		run.ActingUserID == nil || *run.ActingUserID != fx.humanID {
		t.Fatalf("General run correlation = %#v", run)
	}
	var details string
	if err := fx.repo.db.QueryRow(`
		SELECT details FROM audit_logs
		WHERE action_type = 'agent.chat.turn' AND resource_id = ?
	`, session.ID).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, exactBody) {
		t.Fatalf("audit details leaked exact message body: %s", details)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(details), &parsed); err != nil {
		t.Fatal(err)
	}
	if int(parsed["agent_message_id"].(float64)) != begun.MessageID ||
		int(parsed["agent_run_id"].(float64)) != begun.RunID {
		t.Fatalf("audit correlation = %v", parsed)
	}
}

func TestAgentConversationRepositoryCompletesTurnAndUsesServerHistoryOrder(t *testing.T) {
	ctx := context.Background()
	fx := newConversationFixture(t)
	session, err := fx.repo.EnsureGeneralSession(ctx, fx.humanID)
	if err != nil {
		t.Fatal(err)
	}
	begun, err := fx.repo.BeginTurn(ctx, BeginAgentTurnInput{
		SessionID:      session.ID,
		SenderUserID:   fx.humanID,
		SenderUsername: "human",
		ActingUserID:   fx.humanID,
		WorkspaceID:    1,
		JobKind:        models.JobKindGeneralAgent,
		Content:        "first question",
	})
	if err != nil {
		t.Fatal(err)
	}
	if transitioned, err := fx.runs.MarkRunningIfQueued(ctx, begun.RunID, "", time.Now().UTC()); err != nil || !transitioned {
		t.Fatalf("mark running: transitioned=%v err=%v", transitioned, err)
	}
	assistantID, err := fx.repo.CompleteTurn(ctx, session.ID, begun.RunID, fx.humanID,
		"first answer", `{"tool_summaries":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := fx.repo.ListMessagesForParticipant(ctx, session.ID, fx.humanID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" ||
		messages[1].ID != assistantID || messages[1].Role != "assistant" ||
		messages[1].Content != "first answer" {
		t.Fatalf("authoritative history = %#v", messages)
	}
	run, err := fx.runs.Get(ctx, begun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("run status = %q", run.Status)
	}
}

func TestAgentConversationRepositoryBeginTurnRollsBackWhenAuditCannotPersist(t *testing.T) {
	ctx := context.Background()
	fx := newConversationFixture(t)
	if fx.repo.db.GetDriverName() != "sqlite" {
		t.Skip("SQLite trigger injects the audit-write failure; transaction semantics are shared")
	}
	session, err := fx.repo.EnsureGeneralSession(ctx, fx.humanID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.repo.db.Exec(`
		CREATE TRIGGER fail_agent_turn_audit
		BEFORE INSERT ON audit_logs
		WHEN NEW.action_type = 'agent.chat.turn'
		BEGIN
			SELECT RAISE(ABORT, 'injected audit failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	_, err = fx.repo.BeginTurn(ctx, BeginAgentTurnInput{
		SessionID:      session.ID,
		SenderUserID:   fx.humanID,
		SenderUsername: "human",
		ActingUserID:   fx.humanID,
		WorkspaceID:    1,
		JobKind:        models.JobKindGeneralAgent,
		Content:        "must roll back",
	})
	if err == nil {
		t.Fatal("BeginTurn succeeded despite audit failure")
	}
	var messages, runs int
	if err := fx.repo.db.QueryRow(`SELECT COUNT(*) FROM agent_messages WHERE session_id = ?`, session.ID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := fx.repo.db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE session_id = ?`, strconv.Itoa(session.ID)).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || runs != 0 {
		t.Fatalf("failed audit left partial turn: messages=%d runs=%d", messages, runs)
	}
}

func TestAgentConversationRepositoryStandardArchivePreservesTranscriptAndBlocksTurns(t *testing.T) {
	ctx := context.Background()
	fx := newConversationFixture(t)
	profile := &models.WorkspaceAgentBinding{
		ID:           71,
		WorkspaceID:  1,
		ActingUserID: fx.agentID,
		ProfileType:  models.AgentProfileStandard,
		Lifecycle:    models.AgentLifecycleReady,
		DisplayName:  "Review Agent",
	}
	session, err := fx.repo.CreateStandardSession(ctx, fx.humanID, 1, profile, "Review")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Participants) != 2 {
		t.Fatalf("Standard participants = %#v", session.Participants)
	}
	if _, err := fx.repo.GetForParticipant(ctx, session.ID, fx.otherID); !errors.Is(err, ErrAgentSessionNotFound) {
		t.Fatalf("non-participant read error = %v", err)
	}
	archived, err := fx.repo.ArchiveOwnedStandard(ctx, session.ID, fx.humanID)
	if err != nil || !archived {
		t.Fatalf("archive: archived=%v err=%v", archived, err)
	}
	_, err = fx.repo.BeginTurn(ctx, BeginAgentTurnInput{
		SessionID:      session.ID,
		SenderUserID:   fx.humanID,
		SenderUsername: "human",
		ActingUserID:   fx.agentID,
		WorkspaceID:    1,
		JobKind:        models.JobKindStandardAgent,
		Content:        "must not execute",
	})
	if !errors.Is(err, ErrAgentSessionArchived) {
		t.Fatalf("archived turn error = %v", err)
	}
	if _, err := fx.repo.Get(ctx, session.ID); err != nil {
		t.Fatalf("archive hard-deleted session: %v", err)
	}
}
