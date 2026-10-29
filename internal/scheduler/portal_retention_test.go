//go:build test

package scheduler

import (
	"context"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// seedRetentionFixtures seeds a portal customer with sessions, magic links,
// and kb_events spread across the retention boundary. All relative ages are
// measured from now so the sweep cutoff comparisons stay deterministic.
// Returns the plain portal channel (instance default window), the portal
// channel with a 7-day kb_events override, and the customer id.
func seedRetentionFixtures(t *testing.T, db database.Database) (channelPlain, channelShortWindow, customerID int) {
	t.Helper()

	now := time.Now()
	ctx := context.Background()

	insertID := func(query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRowContext(ctx, "INSERT INTO "+query+" RETURNING id", args...).Scan(&id); err != nil {
			t.Fatalf("seed fixture: %v\nquery: %s", err, query)
		}
		return id
	}

	customerID = insertID("portal_customers (name, email) VALUES (?, ?)", "Retention Subject", "retention@test.local")

	channelPlain = insertID("channels (name, type, direction, status, config) VALUES (?, 'portal', 'inbound', 'enabled', '{}')", "retention-plain")
	channelShortWindow = insertID("channels (name, type, direction, status, config) VALUES (?, 'portal', 'inbound', 'enabled', ?)",
		"retention-short", `{"kb_events_retention_days":7}`)

	sessions := map[string]time.Time{
		"live":          now.Add(24 * time.Hour),
		"recent-expiry": now.Add(-5 * 24 * time.Hour),
		"old-expiry":    now.Add(-40 * 24 * time.Hour),
	}
	for name, expiresAt := range sessions {
		insertID("portal_customer_sessions (portal_customer_id, session_token, expires_at, ip_address, user_agent, is_active) VALUES (?, ?, ?, '10.0.0.1', 'ua', ?)",
			customerID, "tok-"+name, expiresAt, true)
	}

	links := []struct {
		token     string
		expiresAt time.Time
		usedAt    *time.Time
	}{
		{"ml-future", now.Add(24 * time.Hour), nil},
		{"ml-used-recent", now.Add(24 * time.Hour), timePtr(now.Add(-5 * 24 * time.Hour))},
		{"ml-used-old", now.Add(24 * time.Hour), timePtr(now.Add(-40 * 24 * time.Hour))},
		{"ml-expired-old", now.Add(-40 * 24 * time.Hour), nil},
	}
	for _, l := range links {
		insertID("portal_customer_magic_links (portal_customer_id, token, expires_at, used_at) VALUES (?, ?, ?, ?)",
			customerID, l.token, l.expiresAt, l.usedAt)
	}

	events := []struct {
		channelID int
		age       time.Duration
		eventType string
	}{
		{channelPlain, 10 * 24 * time.Hour, "search"},
		{channelPlain, 100 * 24 * time.Hour, "search"},
		{channelShortWindow, 3 * 24 * time.Hour, "no_result"},
		{channelShortWindow, 10 * 24 * time.Hour, "view"},
	}
	for _, e := range events {
		insertID("kb_events (channel_id, portal_customer_id, event_type, query, created_at) VALUES (?, ?, ?, ?, ?)",
			e.channelID, customerID, e.eventType, "query", now.Add(-e.age))
	}
	return channelPlain, channelShortWindow, customerID
}

func timePtr(t time.Time) *time.Time { return &t }

func countRows(t *testing.T, db database.Database, tableAndWhere string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+tableAndWhere, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", tableAndWhere, err)
	}
	return n
}

// TestPortalAuthRetentionSweeper_PurgesExpiredKeepsLive pins the grace
// semantics: expired sessions and consumed/expired magic links are purged
// only after the retention window passes, live credentials are untouched,
// system_settings overrides apply, and the tick is recorded for admin
// diagnostics.
func TestPortalAuthRetentionSweeper_PurgesExpiredKeepsLive(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	_, _, customerID := seedRetentionFixtures(t, db)

	sweeper := NewPortalAuthRetentionSweeper(db)
	sweeper.tick()

	// Defaults (30 days): live and 5-day-expired sessions survive; the
	// 40-day-expired session is purged.
	if got := countRows(t, db, "portal_customer_sessions WHERE portal_customer_id = ? AND session_token = 'tok-live'", customerID); got != 1 {
		t.Errorf("live session rows = %d, want 1", got)
	}
	if got := countRows(t, db, "portal_customer_sessions WHERE portal_customer_id = ? AND session_token = 'tok-recent-expiry'", customerID); got != 1 {
		t.Errorf("recent-expiry session rows = %d, want 1 (inside the grace window)", got)
	}
	if got := countRows(t, db, "portal_customer_sessions WHERE portal_customer_id = ? AND session_token = 'tok-old-expiry'", customerID); got != 0 {
		t.Errorf("old-expiry session rows = %d, want 0 (past the grace window)", got)
	}

	// Magic links: unused-future and recently-used survive; used-old and
	// expired-old are purged.
	for token, want := range map[string]int{
		"ml-future":      1,
		"ml-used-recent": 1,
		"ml-used-old":    0,
		"ml-expired-old": 0,
	} {
		if got := countRows(t, db, "portal_customer_magic_links WHERE token = ?", token); got != want {
			t.Errorf("magic link %s rows = %d, want %d", token, got, want)
		}
	}

	// The tick is recorded for admin diagnostics.
	if got := countRows(t, db, "scheduler_runs WHERE scheduler_name = 'portal_auth_retention' AND success = TRUE"); got != 1 {
		t.Errorf("portal_auth_retention scheduler_runs = %d, want 1", got)
	}

	// An instance override shortens the window: the 5-day-expired session
	// falls outside a 3-day window on the next tick.
	if _, err := db.Exec(`INSERT INTO system_settings (key, value, value_type) VALUES ('portal_session_retention_days', '3', 'integer')`); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	sweeper.tick()
	if got := countRows(t, db, "portal_customer_sessions WHERE portal_customer_id = ? AND session_token = 'tok-recent-expiry'", customerID); got != 0 {
		t.Errorf("recent-expiry session rows after override = %d, want 0 (3-day window)", got)
	}
}

// TestKBEventsRetentionSweeper_PerChannelAndInstanceDefault pins the kb_events
// retention matrix: per-channel overrides win, unset channels use the
// instance default, and the system_settings key replaces the built-in
// default.
func TestKBEventsRetentionSweeper_PerChannelAndInstanceDefault(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	channelPlain, channelShortWindow, _ := seedRetentionFixtures(t, db)

	sweeper := NewKBEventsRetentionSweeper(db)
	sweeper.tick()

	// Plain channel uses the 365-day default: both rows survive.
	if got := countRows(t, db, "kb_events WHERE channel_id = ?", channelPlain); got != 2 {
		t.Errorf("plain channel kb_events = %d, want 2 (instance default window)", got)
	}
	// Override channel (7 days): the 10-day-old row is purged, 3-day kept.
	if got := countRows(t, db, "kb_events WHERE channel_id = ? AND event_type = 'view'", channelShortWindow); got != 0 {
		t.Errorf("short-window channel old row = %d, want 0", got)
	}
	if got := countRows(t, db, "kb_events WHERE channel_id = ? AND event_type = 'no_result'", channelShortWindow); got != 1 {
		t.Errorf("short-window channel recent row = %d, want 1", got)
	}
	if got := countRows(t, db, "scheduler_runs WHERE scheduler_name = 'kb_events_retention' AND success = TRUE"); got != 1 {
		t.Errorf("kb_events_retention scheduler_runs = %d, want 1", got)
	}

	// The instance default is overridable via system_settings: a 50-day
	// window purges the plain channel's 100-day-old row.
	if _, err := db.Exec(`INSERT INTO system_settings (key, value, value_type) VALUES ('kb_events_retention_days', '50', 'integer')`); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	sweeper.tick()
	if got := countRows(t, db, "kb_events WHERE channel_id = ? AND event_type = 'search'", channelPlain); got != 1 {
		t.Errorf("plain channel kb_events after instance override = %d, want 1 (100-day row purged, 10-day kept)", got)
	}
}
