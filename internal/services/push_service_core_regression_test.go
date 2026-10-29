package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/config"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func newPushTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatalf("new SQLite database: %v", err)
	}
	statements := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, is_active BOOLEAN NOT NULL DEFAULT true)`,
		`CREATE TABLE push_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			endpoint TEXT NOT NULL UNIQUE,
			auth_key TEXT NOT NULL,
			p256dh_key TEXT NOT NULL,
			user_agent TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_used_at DATETIME,
			revoked_at DATETIME
		)`,
		`INSERT INTO users (id) VALUES (1), (2), (3)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecWrite(statement); err != nil {
			_ = db.Close()
			t.Fatalf("initialize push database: %v", err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func enabledPushConfig() config.PushConfig {
	return config.PushConfig{
		VAPIDPublicKey:  "public",
		VAPIDPrivateKey: "private",
		VAPIDSubject:    "mailto:test@example.com",
	}
}

func addPushSubscriptions(t *testing.T, db database.Database, userID, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		if _, err := db.ExecWrite(`
			INSERT INTO push_subscriptions (user_id, endpoint, auth_key, p256dh_key)
			VALUES (?, ?, 'auth', 'p256dh')
		`, userID, fmt.Sprintf("https://push.example/%d/%d", userID, i)); err != nil {
			t.Fatalf("insert push subscription: %v", err)
		}
	}
}

func pushOKResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusCreated,
		Status:     "201 Created",
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestPushServiceBoundsQueueWithoutGoroutinePerNotification(t *testing.T) {
	db := newPushTestDB(t)
	addPushSubscriptions(t, db, 1, 1)
	serviceConfig := DefaultPushServiceConfig()
	serviceConfig.QueueSize = 2
	serviceConfig.Workers = 1
	serviceConfig.MaxRetries = 0
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	sender := func(ctx context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			return pushOKResponse(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	service := newPushService(db, enabledPushConfig(), serviceConfig, sender)

	if !service.Enqueue(models.Notification{ID: 1, UserID: 1, Title: "one", AuthorizationScope: models.NotificationScopeSystem}) {
		t.Fatal("first push rejected")
	}
	<-started
	if !service.Enqueue(models.Notification{ID: 2, UserID: 1, Title: "two", AuthorizationScope: models.NotificationScopeSystem}) ||
		!service.Enqueue(models.Notification{ID: 3, UserID: 1, Title: "three", AuthorizationScope: models.NotificationScopeSystem}) {
		t.Fatal("queue rejected within capacity")
	}
	if service.Enqueue(models.Notification{ID: 4, UserID: 1, Title: "four", AuthorizationScope: models.NotificationScopeSystem}) {
		t.Fatal("queue accepted beyond capacity")
	}
	stats := service.GetStats()
	if stats["queue_depth"] != 2 || stats["jobs_dropped"] != 1 || stats["max_active_workers"] != 1 {
		t.Fatalf("push stats = %+v", stats)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatalf("close push service: %v", err)
	}
}

func TestPushServiceReauthorizesQueuedWorkspaceNotification(t *testing.T) {
	env := newPermTestEnv(t)
	userID := env.insertUser("push-recipient@example.com")
	keeperID := env.insertUser("push-keeper@example.com")
	workspaceID := env.insertWorkspace("PUSH-REVOKED")
	viewerRoleID := env.roleID("Viewer")
	roles := repository.NewWorkspaceRoleRepository(env.db)
	for _, id := range []int{userID, keeperID} {
		if err := roles.AssignToUser(id, workspaceID, viewerRoleID, keeperID); err != nil {
			t.Fatalf("assign workspace role: %v", err)
		}
	}
	workspaceIDs, err := env.service.AccessibleWorkspaceIDs(userID)
	if err != nil {
		t.Fatalf("warm workspace scope: %v", err)
	}
	if len(workspaceIDs) != 1 || workspaceIDs[0] != workspaceID {
		t.Fatalf("warm workspace scope = %v, want [%d]", workspaceIDs, workspaceID)
	}
	addPushSubscriptions(t, env.db, userID, 1)

	serviceConfig := DefaultPushServiceConfig()
	serviceConfig.Workers = 1
	serviceConfig.MaxRetries = 0
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	sender := func(ctx context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return pushOKResponse(), nil
	}
	service := newPushService(env.db, enabledPushConfig(), serviceConfig, sender, env.service)
	if !service.Enqueue(models.Notification{
		ID: 1, UserID: userID, Title: "blocker", AuthorizationScope: models.NotificationScopeSystem,
	}) {
		t.Fatal("enqueue blocker push")
	}
	<-started
	if !service.Enqueue(models.Notification{
		ID: 2, UserID: userID, Title: "restricted", AuthorizationScope: models.NotificationScopeWorkspace, WorkspaceID: &workspaceID,
	}) {
		t.Fatal("enqueue workspace push")
	}
	if _, err := roles.RevokeFromUser(userID, workspaceID, viewerRoleID); err != nil {
		t.Fatalf("revoke workspace role: %v", err)
	}
	if err := env.service.InvalidateUserCache(userID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatalf("close push service: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("push sender calls = %d, want blocker only", got)
	}
}

func TestPushServiceReauthorizesQueuedAssetNotification(t *testing.T) {
	env := newPermTestEnv(t)
	userID := env.insertUser("asset-push-recipient@example.com")
	keeperID := env.insertUser("asset-push-keeper@example.com")
	insertID := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := env.db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	setID := insertID("asset set", `INSERT INTO asset_management_sets (name, created_by) VALUES ('Push notification assets', ?) RETURNING id`, keeperID)
	typeID := insertID("asset type", `INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id`, setID)
	assetID := insertID("asset", `INSERT INTO assets (set_id, asset_type_id, title, created_by) VALUES (?, ?, 'Restricted push server', ?) RETURNING id`, setID, typeID, keeperID)
	viewerRoleID := insertID("viewer role", `SELECT id FROM asset_roles WHERE name = 'Viewer'`)
	if _, err := env.db.Exec(`INSERT INTO user_asset_set_roles (user_id, set_id, role_id, granted_by) VALUES (?, ?, ?, ?)`, userID, setID, viewerRoleID, keeperID); err != nil {
		t.Fatalf("grant asset view: %v", err)
	}
	addPushSubscriptions(t, env.db, userID, 1)

	serviceConfig := DefaultPushServiceConfig()
	serviceConfig.Workers = 1
	serviceConfig.MaxRetries = 0
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	sender := func(ctx context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return pushOKResponse(), nil
	}
	service := newPushService(env.db, enabledPushConfig(), serviceConfig, sender, env.service)
	service.SetNotificationAuthorizer(NewNotificationAuthorizer(
		env.db,
		env.service,
		NewAssetPermissionService(repository.NewAssetRepository(env.db), env.service),
	))
	if !service.Enqueue(models.Notification{ID: 1, UserID: userID, Title: "blocker", AuthorizationScope: models.NotificationScopeSystem}) {
		t.Fatal("enqueue blocker push")
	}
	<-started
	if !service.Enqueue(models.Notification{ID: 2, UserID: userID, Title: "restricted asset", AuthorizationScope: models.NotificationScopeAsset, SourceType: "asset", SourceID: &assetID}) {
		t.Fatal("enqueue asset push")
	}
	if _, err := env.db.Exec(`DELETE FROM user_asset_set_roles WHERE user_id = ? AND set_id = ?`, userID, setID); err != nil {
		t.Fatalf("revoke asset view: %v", err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatalf("close push service: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("push sender calls = %d, want blocker only", got)
	}
}

func TestPushServiceReauthorizesQueuedReferencedWorkspaceNotification(t *testing.T) {
	env := newPermTestEnv(t)
	userID := env.insertUser("linked-push-recipient@example.com")
	keeperID := env.insertUser("linked-push-keeper@example.com")
	primaryWorkspaceID := env.insertWorkspace("LINK-PUSH-PRIMARY")
	secondaryWorkspaceID := env.insertWorkspace("LINK-PUSH-SECONDARY")
	viewerRoleID := env.roleID("Viewer")
	roles := repository.NewWorkspaceRoleRepository(env.db)
	for _, workspaceID := range []int{primaryWorkspaceID, secondaryWorkspaceID} {
		if err := roles.AssignToUser(userID, workspaceID, viewerRoleID, keeperID); err != nil {
			t.Fatalf("assign recipient workspace role: %v", err)
		}
		if err := roles.AssignToUser(keeperID, workspaceID, viewerRoleID, keeperID); err != nil {
			t.Fatalf("assign keeper workspace role: %v", err)
		}
	}
	targetID64, err := CreateItem(env.db, ItemCreationParams{WorkspaceID: secondaryWorkspaceID, Title: "Secret push target"})
	if err != nil {
		t.Fatalf("create linked target: %v", err)
	}
	targetID := int(targetID64)
	addPushSubscriptions(t, env.db, userID, 1)

	serviceConfig := DefaultPushServiceConfig()
	serviceConfig.Workers = 1
	serviceConfig.MaxRetries = 0
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	sender := func(ctx context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return pushOKResponse(), nil
	}
	service := newPushService(env.db, enabledPushConfig(), serviceConfig, sender, env.service)
	if !service.Enqueue(models.Notification{ID: 1, UserID: userID, Title: "blocker", AuthorizationScope: models.NotificationScopeSystem}) {
		t.Fatal("enqueue blocker push")
	}
	<-started
	if !service.Enqueue(models.Notification{
		ID: userID, UserID: userID, Title: "Secret push target",
		AuthorizationScope:            models.NotificationScopeWorkspace,
		WorkspaceID:                   &primaryWorkspaceID,
		SourceType:                    models.EventItemLinked,
		ReferencedEntityType:          "item",
		ReferencedEntityID:            &targetID,
		ReferencedWorkspaceID:         &secondaryWorkspaceID,
		ReferencedWorkspacePermission: models.PermissionItemView,
	}) {
		t.Fatal("enqueue linked push")
	}
	if _, err := roles.RevokeFromUser(userID, secondaryWorkspaceID, viewerRoleID); err != nil {
		t.Fatalf("revoke secondary workspace: %v", err)
	}
	if err := env.service.InvalidateUserCache(userID); err != nil {
		t.Fatalf("invalidate secondary revocation: %v", err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatalf("close push service: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("push sender calls = %d, want blocker only", got)
	}
}

func TestPushServiceRetriesAndCapsSubscriptionFanout(t *testing.T) {
	db := newPushTestDB(t)
	addPushSubscriptions(t, db, 1, 5)
	serviceConfig := DefaultPushServiceConfig()
	serviceConfig.Workers = 1
	serviceConfig.MaxSubscriptions = 2
	serviceConfig.MaxRetries = 1
	serviceConfig.RetryInitialBackoff = time.Millisecond
	var attempts sync.Map
	sender := func(_ context.Context, _ []byte, subscription *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		value, _ := attempts.LoadOrStore(subscription.Endpoint, new(atomic.Int64))
		if value.(*atomic.Int64).Add(1) == 1 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Status:     "503 Service Unavailable",
				Body:       io.NopCloser(strings.NewReader("retry")),
			}, nil
		}
		return pushOKResponse(), nil
	}
	service := newPushService(db, enabledPushConfig(), serviceConfig, sender)
	if !service.Enqueue(models.Notification{ID: 1, UserID: 1, Title: "fanout", AuthorizationScope: models.NotificationScopeSystem}) {
		t.Fatal("push rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatalf("close push service: %v", err)
	}
	stats := service.GetStats()
	if stats["subscriptions_sent"] != 2 || stats["subscriptions_dropped"] != 3 || stats["retries"] != 2 {
		t.Fatalf("push stats = %+v", stats)
	}
}

func TestPushServiceSerializesSameUserAndBoundsGlobalWorkers(t *testing.T) {
	db := newPushTestDB(t)
	addPushSubscriptions(t, db, 1, 1)
	addPushSubscriptions(t, db, 2, 1)
	serviceConfig := DefaultPushServiceConfig()
	serviceConfig.Workers = 2
	serviceConfig.MaxRetries = 0
	var mu sync.Mutex
	activeByEndpoint := map[string]int{}
	maxByEndpoint := map[string]int{}
	var active, maxActive atomic.Int64
	sender := func(_ context.Context, _ []byte, subscription *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		mu.Lock()
		activeByEndpoint[subscription.Endpoint]++
		if activeByEndpoint[subscription.Endpoint] > maxByEndpoint[subscription.Endpoint] {
			maxByEndpoint[subscription.Endpoint] = activeByEndpoint[subscription.Endpoint]
		}
		mu.Unlock()
		<-time.After(20 * time.Millisecond)
		mu.Lock()
		activeByEndpoint[subscription.Endpoint]--
		mu.Unlock()
		active.Add(-1)
		return pushOKResponse(), nil
	}
	service := newPushService(db, enabledPushConfig(), serviceConfig, sender)
	service.Enqueue(models.Notification{ID: 1, UserID: 1, Title: "one", AuthorizationScope: models.NotificationScopeSystem})
	service.Enqueue(models.Notification{ID: 2, UserID: 2, Title: "two", AuthorizationScope: models.NotificationScopeSystem})
	service.Enqueue(models.Notification{ID: 3, UserID: 1, Title: "three", AuthorizationScope: models.NotificationScopeSystem})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatalf("close push service: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for endpoint, maximum := range maxByEndpoint {
		if maximum != 1 {
			t.Fatalf("endpoint %s concurrency = %d", endpoint, maximum)
		}
	}
	if maxActive.Load() > int64(serviceConfig.Workers) {
		t.Fatalf("global sender concurrency = %d", maxActive.Load())
	}
	if maxActive.Load() < 2 {
		t.Fatalf("independent users did not run concurrently: max=%d", maxActive.Load())
	}
}

func TestPushServiceShutdownDeadlineReportsUndrainedWork(t *testing.T) {
	db := newPushTestDB(t)
	addPushSubscriptions(t, db, 1, 1)
	serviceConfig := DefaultPushServiceConfig()
	serviceConfig.Workers = 1
	serviceConfig.DeliveryTimeout = time.Hour
	started := make(chan struct{})
	var once sync.Once
	sender := func(ctx context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	service := newPushService(db, enabledPushConfig(), serviceConfig, sender)
	service.Enqueue(models.Notification{ID: 1, UserID: 1, Title: "blocked", AuthorizationScope: models.NotificationScopeSystem})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := service.Close(ctx); err == nil {
		t.Fatal("shutdown deadline returned nil")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && service.GetStats()["active_workers"] != 0 {
		<-time.After(time.Millisecond)
	}
	if active := service.GetStats()["active_workers"]; active != 0 {
		t.Fatalf("active workers after cancellation = %d", active)
	}
}
