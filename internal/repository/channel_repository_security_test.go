package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
)

func newChannelRepositoryTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "channels.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	if err := db.Initialize(); err != nil {
		_ = db.Close()
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertTestChannel(t *testing.T, db database.Database, channelType, direction, status, config string, isDefault bool) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, is_default, config)
		VALUES ('test', ?, ?, ?, ?, ?) RETURNING id
	`, channelType, direction, status, isDefault, config).Scan(&id); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return id
}

func TestScrubChannelConfigFailsClosed(t *testing.T) {
	for _, input := range []string{"not-json", "null"} {
		if got := ScrubChannelConfig(input); got != "{}" {
			t.Fatalf("ScrubChannelConfig(%q) = %q, want {}", input, got)
		}
	}
}

func TestSlugInUseIncludesDisabledChannels(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	firstID := insertTestChannel(t, db, "portal", "inbound", "disabled", `{"portal_slug":"support"}`, false)
	secondID := insertTestChannel(t, db, "portal", "inbound", "disabled", `{}`, false)

	inUse, err := NewChannelRepository(db).SlugInUse(context.Background(), "portal", "support", secondID)
	if err != nil {
		t.Fatalf("SlugInUse: %v", err)
	}
	if !inUse {
		t.Fatalf("slug from disabled channel %d was not treated as in use", firstID)
	}
}

func TestFindEnabledByPublicSlugUsesNormalizedSlug(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{}`, false)
	repo := NewChannelRepository(db)
	if err := database.WithTx(db, func(tx database.Tx) error {
		return repo.UpdateConfig(context.Background(), tx, channelID, `{"portal_slug":"support"}`)
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	candidate, err := repo.FindEnabledByPublicSlug(context.Background(), "portal", "support")
	if err != nil {
		t.Fatalf("FindEnabledByPublicSlug: %v", err)
	}
	if candidate.Channel.ID != channelID || candidate.Config.PortalSlug != "support" {
		t.Fatalf("candidate = %+v, want channel %d/support", candidate, channelID)
	}

	if _, err := db.ExecWrite("UPDATE channels SET status = 'disabled' WHERE id = ?", channelID); err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	if _, err := repo.FindEnabledByPublicSlug(context.Background(), "portal", "support"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled channel lookup error = %v, want ErrNotFound", err)
	}
}

func TestSlugInUseIgnoresMalformedLegacyConfig(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	insertTestChannel(t, db, "portal", "inbound", "disabled", `not-json`, false)
	insertTestChannel(t, db, "portal", "inbound", "disabled", `{"portal_slug":"support"}`, false)

	inUse, err := NewChannelRepository(db).SlugInUse(context.Background(), "portal", "support", 0)
	if err != nil {
		t.Fatalf("SlugInUse: %v", err)
	}
	if !inUse {
		t.Fatal("valid slug after malformed legacy config was not found")
	}
}

func TestEmailLogAcceptsNullSubject(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "email", "inbound", "enabled", `{}`, false)
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, subject)
		VALUES (?, 'message-1', 'message-1', 'sender@example.com', NULL)
	`, channelID); err != nil {
		t.Fatalf("insert tracking row: %v", err)
	}

	rows, err := NewChannelRepository(db).ListEmailMessages(context.Background(), channelID, "", 1, 50)
	if err != nil {
		t.Fatalf("ListEmailMessages: %v", err)
	}
	if len(rows) != 1 || rows[0].Subject != "" {
		t.Fatalf("rows = %#v, want one row with empty subject", rows)
	}
}

func TestInlineOAuthStateUsesNullableProviderAndIsSingleUse(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	if _, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('oauth@example.com', 'oauth-user', 'OAuth', 'User')
	`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var userID int
	if err := db.QueryRow("SELECT id FROM users WHERE username = 'oauth-user'").Scan(&userID); err != nil {
		t.Fatalf("load user: %v", err)
	}
	channelID := insertTestChannel(t, db, "email", "inbound", "disabled", `{}`, false)
	repo := NewChannelRepository(db)
	if err := repo.CreateOAuthState(context.Background(), "one-time-state", channelID, userID, true, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("CreateOAuthState: %v", err)
	}
	providerID, gotChannelID, gotUserID, restoreEnabled, err := repo.ConsumeOAuthState(context.Background(), "one-time-state", false)
	if err != nil {
		t.Fatalf("ConsumeOAuthState: %v", err)
	}
	if providerID != 0 || gotChannelID != channelID || gotUserID != userID || !restoreEnabled {
		t.Fatalf("consumed state = (%d, %d, %d, %t), want (0, %d, %d, true)", providerID, gotChannelID, gotUserID, restoreEnabled, channelID, userID)
	}
	if _, _, _, _, err := repo.ConsumeOAuthState(context.Background(), "one-time-state", false); err == nil {
		t.Fatal("replayed OAuth state unexpectedly succeeded")
	}
}

func TestDefaultChannelUniquePerRoute(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	insertTestChannel(t, db, "widget", "outbound", "enabled", `{}`, true)
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, is_default, config)
		VALUES ('second', 'widget', 'outbound', 'enabled', true, '{}') RETURNING id
	`).Scan(new(int)); err == nil {
		t.Fatal("second default channel for the same type/direction unexpectedly succeeded")
	}
}

func TestDeleteRejectsDefaultChannelInsideTransaction(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{}`, true)
	if _, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('manager@example.com', 'manager', 'Channel', 'Manager')
	`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO channel_managers (channel_id, manager_type, manager_id)
		SELECT ?, 'user', id FROM users WHERE username = 'manager'
	`, channelID); err != nil {
		t.Fatalf("insert manager: %v", err)
	}

	repo := NewChannelRepository(db)
	err := database.WithTx(db, func(tx database.Tx) error {
		return repo.Delete(context.Background(), tx, channelID)
	})
	if !errors.Is(err, ErrDefaultChannel) {
		t.Fatalf("Delete error = %v, want ErrDefaultChannel", err)
	}

	var managerCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM channel_managers WHERE channel_id = ?", channelID).Scan(&managerCount); err != nil {
		t.Fatalf("count managers: %v", err)
	}
	if managerCount != 1 {
		t.Fatalf("manager count = %d, want rollback to preserve 1", managerCount)
	}
}

func TestAddManagerReportsConflictNoOp(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "form", "inbound", "enabled", `{}`, false)
	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('manager@example.com', 'manager', 'Channel', 'Manager') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	repo := NewChannelRepository(db)
	add := func() bool {
		t.Helper()
		created, err := database.WithTxResult(db, func(tx database.Tx) (bool, error) {
			return repo.AddManager(context.Background(), tx, channelID, "user", userID, userID)
		})
		if err != nil {
			t.Fatalf("AddManager: %v", err)
		}
		return created
	}
	if !add() {
		t.Fatal("first manager insert was reported as a no-op")
	}
	if add() {
		t.Fatal("duplicate manager insert was reported as newly created")
	}
}

func TestInactiveGroupCannotManageChannel(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{}`, false)
	var userID, groupID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('inactive-manager@example.com', 'inactive-manager', 'Inactive', 'Manager') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO groups (name, is_active) VALUES ('Inactive managers', false) RETURNING id`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO channel_managers (channel_id, manager_type, manager_id, added_by)
		VALUES (?, 'group', ?, ?)
	`, channelID, groupID, userID); err != nil {
		t.Fatalf("insert channel manager: %v", err)
	}

	repo := NewChannelRepository(db)
	canManage, err := repo.UserCanManage(context.Background(), userID, channelID)
	if err != nil {
		t.Fatalf("UserCanManage: %v", err)
	}
	if canManage {
		t.Fatal("inactive group membership granted channel management")
	}
	managesAny, err := repo.UserManagesAny(context.Background(), userID)
	if err != nil {
		t.Fatalf("UserManagesAny: %v", err)
	}
	if managesAny {
		t.Fatal("inactive group membership reported channel management availability")
	}
	channels, err := repo.FindAll(context.Background(), userID, false, ChannelListFilters{IncludeDisabled: true})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("inactive group exposed managed channels: %#v", channels)
	}
}

func TestUserManagesAnyIncludesDirectAndActiveGroupAssignmentsButExcludesDefaults(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	repo := NewChannelRepository(db)
	defaultChannelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{}`, true)
	directChannelID := insertTestChannel(t, db, "form", "inbound", "disabled", `{}`, false)
	groupChannelID := insertTestChannel(t, db, "email", "inbound", "enabled", `{}`, false)

	var defaultOnlyUserID, directUserID, groupUserID, groupID int
	for email, target := range map[string]*int{
		"default-only@example.com": &defaultOnlyUserID,
		"direct@example.com":       &directUserID,
		"group@example.com":        &groupUserID,
	} {
		if err := db.QueryRow(`
			INSERT INTO users (email, username, first_name, last_name)
			VALUES (?, ?, 'Channel', 'Manager') RETURNING id
		`, email, email).Scan(target); err != nil {
			t.Fatalf("insert user %s: %v", email, err)
		}
	}
	if err := db.QueryRow(`
		INSERT INTO groups (name, is_active)
		VALUES ('Active managers', true) RETURNING id
	`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, groupUserID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}
	for _, assignment := range []struct {
		channelID   int
		managerType string
		managerID   int
	}{
		{defaultChannelID, "user", defaultOnlyUserID},
		{directChannelID, "user", directUserID},
		{groupChannelID, "group", groupID},
	} {
		if _, err := db.ExecWrite(`
			INSERT INTO channel_managers (channel_id, manager_type, manager_id)
			VALUES (?, ?, ?)
		`, assignment.channelID, assignment.managerType, assignment.managerID); err != nil {
			t.Fatalf("insert %s manager: %v", assignment.managerType, err)
		}
	}

	for _, test := range []struct {
		name   string
		userID int
		want   bool
	}{
		{name: "default only", userID: defaultOnlyUserID, want: false},
		{name: "direct disabled channel", userID: directUserID, want: true},
		{name: "active group", userID: groupUserID, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := repo.UserManagesAny(context.Background(), test.userID)
			if err != nil {
				t.Fatalf("UserManagesAny: %v", err)
			}
			if got != test.want {
				t.Fatalf("UserManagesAny = %t, want %t", got, test.want)
			}
		})
	}
}

func TestFindAllHidesDefaultChannelsFromNonAdminManagers(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	repo := NewChannelRepository(db)
	defaultChannelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{}`, true)
	managedChannelID := insertTestChannel(t, db, "form", "inbound", "enabled", `{}`, false)

	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('default-manager@example.com', 'default-manager', 'Default', 'Manager')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	for _, channelID := range []int{defaultChannelID, managedChannelID} {
		if _, err := db.ExecWrite(`
			INSERT INTO channel_managers (channel_id, manager_type, manager_id)
			VALUES (?, 'user', ?)
		`, channelID, userID); err != nil {
			t.Fatalf("insert channel %d manager: %v", channelID, err)
		}
	}

	managed, err := repo.FindAll(context.Background(), userID, false, ChannelListFilters{IncludeDisabled: true})
	if err != nil {
		t.Fatalf("FindAll for manager: %v", err)
	}
	if len(managed) != 1 || managed[0].ID != managedChannelID {
		t.Fatalf("manager channels = %#v, want only non-default channel %d", managed, managedChannelID)
	}

	admin, err := repo.FindAll(context.Background(), userID, true, ChannelListFilters{IncludeDisabled: true})
	if err != nil {
		t.Fatalf("FindAll for admin: %v", err)
	}
	visible := make(map[int]bool, len(admin))
	for _, channel := range admin {
		visible[channel.ID] = true
	}
	if !visible[defaultChannelID] || !visible[managedChannelID] {
		t.Fatalf("admin channels = %#v, want target channels %d and %d", admin, defaultChannelID, managedChannelID)
	}
}

func TestCountManagersIgnoresGroupsWithoutActiveMembers(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{}`, false)
	var userID, groupID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('inactive-member@example.com', 'inactive-member', 'Inactive', 'Member', false)
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert inactive user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO groups (name, is_active)
		VALUES ('Empty managers', true) RETURNING id
	`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO channel_managers (channel_id, manager_type, manager_id)
		VALUES (?, 'group', ?)
	`, channelID, groupID); err != nil {
		t.Fatalf("insert group manager: %v", err)
	}

	repo := NewChannelRepository(db)
	count := func() int {
		t.Helper()
		got, err := database.WithTxResult(db, func(tx database.Tx) (int, error) {
			return repo.CountManagers(context.Background(), tx, channelID)
		})
		if err != nil {
			t.Fatalf("CountManagers: %v", err)
		}
		return got
	}

	if got := count(); got != 0 {
		t.Fatalf("empty active group counted as %d effective managers, want 0", got)
	}
	if _, err := db.ExecWrite(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		t.Fatalf("insert inactive group member: %v", err)
	}
	if got := count(); got != 0 {
		t.Fatalf("group with only inactive members counted as %d effective managers, want 0", got)
	}
	if _, err := db.ExecWrite(`UPDATE users SET is_active = true WHERE id = ?`, userID); err != nil {
		t.Fatalf("activate group member: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("group with an active member counted as %d effective managers, want 1", got)
	}
}

func TestUpdateConfigIfUnchangedRejectsStaleWriter(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{"portal_slug":"initial"}`, false)
	repo := NewChannelRepository(db)
	update := func(expected, next string) bool {
		t.Helper()
		updated, err := database.WithTxResult(db, func(tx database.Tx) (bool, error) {
			return repo.UpdateConfigIfUnchanged(context.Background(), tx, channelID, expected, "enabled", next)
		})
		if err != nil {
			t.Fatalf("UpdateConfigIfUnchanged: %v", err)
		}
		return updated
	}
	initial := `{"portal_slug":"initial"}`
	if !update(initial, `{"portal_slug":"winner"}`) {
		t.Fatal("current config writer was rejected")
	}
	if update(initial, `{"portal_slug":"stale"}`) {
		t.Fatal("stale config writer overwrote a concurrent update")
	}
	if _, err := db.ExecWrite("UPDATE channels SET status = 'disabled' WHERE id = ?", channelID); err != nil {
		t.Fatalf("change status: %v", err)
	}
	if update(`{"portal_slug":"winner"}`, `{"portal_slug":"status-race"}`) {
		t.Fatal("config writer with stale status unexpectedly succeeded")
	}
	var got string
	if err := db.QueryRow("SELECT config FROM channels WHERE id = ?", channelID).Scan(&got); err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got != `{"portal_slug":"winner"}` {
		t.Fatalf("config = %q, want winner", got)
	}
}

func TestPublicSlugUniqueIndexRejectsRacingConfigWriter(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	firstID := insertTestChannel(t, db, "portal", "inbound", "disabled", `{}`, false)
	secondID := insertTestChannel(t, db, "portal", "inbound", "disabled", `{}`, false)
	repo := NewChannelRepository(db)

	update := func(channelID int, config string) (bool, error) {
		return database.WithTxResult(db, func(tx database.Tx) (bool, error) {
			return repo.UpdateConfigIfUnchanged(context.Background(), tx, channelID, `{}`, "disabled", config)
		})
	}
	updated, err := update(firstID, `{"portal_slug":"support"}`)
	if err != nil || !updated {
		t.Fatalf("first slug update = (%t, %v), want success", updated, err)
	}
	if _, err := update(secondID, `{"portal_slug":"support"}`); !errors.Is(err, ErrChannelSlugConflict) {
		t.Fatalf("second slug update error = %v, want ErrChannelSlugConflict", err)
	}

	var raw string
	var publicSlug *string
	if err := db.QueryRow("SELECT config, public_slug FROM channels WHERE id = ?", secondID).Scan(&raw, &publicSlug); err != nil {
		t.Fatalf("load rejected channel: %v", err)
	}
	if raw != `{}` || publicSlug != nil {
		t.Fatalf("rejected channel mutated to config=%q public_slug=%v", raw, publicSlug)
	}

	// Portal and form routes have different URL namespaces, so the same slug
	// remains valid across the two channel types.
	formID := insertTestChannel(t, db, "form", "inbound", "disabled", `{}`, false)
	if updated, err := update(formID, `{"form_slug":"support"}`); err != nil || !updated {
		t.Fatalf("form slug update = (%t, %v), want success", updated, err)
	}
}

func TestRequestTypeDeleteCleansPortalReferenceWithoutDroppingUnknownConfig(t *testing.T) {
	db := newChannelRepositoryTestDB(t)
	channelID := insertTestChannel(t, db, "portal", "inbound", "enabled", `{}`, false)
	var workspaceID, itemTypeID, requestTypeID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Cleanup', 'CLN') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Cleanup Type') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id)
		VALUES (?, 'Cleanup Request', ?, ?) RETURNING id
	`, channelID, itemTypeID, workspaceID).Scan(&requestTypeID); err != nil {
		t.Fatalf("insert request type: %v", err)
	}
	config := fmt.Sprintf(`{
		"future_setting":{"enabled":true},
		"portal_sections":[{
			"id":"primary",
			"title":"Primary",
			"request_type_ids":[%d,999],
			"asset_report_ids":[],
			"future_layout":"tiles"
		}]
	}`, requestTypeID)
	if _, err := db.ExecWrite("UPDATE channels SET config = ? WHERE id = ?", config, channelID); err != nil {
		t.Fatalf("set portal config: %v", err)
	}

	if err := NewRequestTypeRepository(db).Delete(requestTypeID, channelID); err != nil {
		t.Fatalf("Delete request type: %v", err)
	}
	var raw string
	if err := db.QueryRow("SELECT config FROM channels WHERE id = ?", channelID).Scan(&raw); err != nil {
		t.Fatalf("load portal config: %v", err)
	}
	var got struct {
		FutureSetting map[string]bool `json:"future_setting"`
		Sections      []struct {
			RequestTypeIDs []int  `json:"request_type_ids"`
			FutureLayout   string `json:"future_layout"`
		} `json:"portal_sections"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("parse cleaned config: %v", err)
	}
	if !got.FutureSetting["enabled"] {
		t.Fatal("unknown top-level config was dropped")
	}
	if len(got.Sections) != 1 || got.Sections[0].FutureLayout != "tiles" {
		t.Fatalf("unknown portal-section config was dropped: %#v", got.Sections)
	}
	if ids := got.Sections[0].RequestTypeIDs; len(ids) != 1 || ids[0] != 999 {
		t.Fatalf("request_type_ids after cleanup = %v, want [999]", ids)
	}
}
