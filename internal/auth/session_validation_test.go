package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/cacheutil"
	"windshift/internal/database"
)

const sessionValidationTestIP = "198.51.100.10"

func newSessionValidationTestManager(t testing.TB, ttl time.Duration) (database.Database, *SessionManager, *Session) {
	t.Helper()

	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	result, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active, email_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, "cache-test@example.com", "cache-test", "Cache", "Test")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	manager := NewSessionManagerWithValidationCacheTTL(
		db,
		false,
		false,
		nil,
		"test-cookie-secret",
		"strict",
		ttl,
	)
	session, err := manager.CreateSession(int(userID), sessionValidationTestIP, "test-agent", false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return db, manager, session
}

func BenchmarkSessionValidation(b *testing.B) {
	for _, benchmark := range []struct {
		name string
		ttl  time.Duration
	}{
		{name: "database_every_request", ttl: 0},
		{name: "validation_cache", ttl: time.Minute},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			_, manager, session := newSessionValidationTestManager(b, benchmark.ttl)
			b.ResetTimer()
			for range b.N {
				if _, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP); err != nil {
					b.Fatalf("ValidateSessionContext: %v", err)
				}
			}
			b.StopTimer()
			loads := manager.SessionValidationCacheStats().DatabaseLoads
			b.ReportMetric(float64(loads)/float64(b.N), "db-loads/op")
		})
	}
}

func TestSessionValidationCacheReusesImmutableSnapshotAndChecksIP(t *testing.T) {
	_, manager, session := newSessionValidationTestManager(t, time.Minute)

	first, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("first ValidateSessionContext: %v", err)
	}
	first.User.FirstName = "mutated by caller"

	second, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("cached ValidateSessionContext: %v", err)
	}
	if second.User.FirstName != "Cache" {
		t.Fatalf("cached user was mutable across callers: %q", second.User.FirstName)
	}
	if _, err := manager.ValidateSessionContext(context.Background(), session.Token, "203.0.113.7"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("cached validation with wrong IP = %v, want ErrInvalidSession", err)
	}

	stats := manager.SessionValidationCacheStats()
	if stats.DatabaseLoads != 1 || stats.Hits != 2 || stats.Entries != 1 {
		t.Fatalf("unexpected cache stats: %+v", stats)
	}
}

func TestSessionValidationCacheHonorsExactTTL(t *testing.T) {
	db, manager, session := newSessionValidationTestManager(t, time.Hour)

	first, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("first ValidateSessionContext: %v", err)
	}
	if first.User.FirstName != "Cache" {
		t.Fatalf("first name = %q, want Cache", first.User.FirstName)
	}
	if _, err := db.ExecWrite(`UPDATE users SET first_name = ? WHERE id = ?`, "Updated", session.UserID); err != nil {
		t.Fatalf("update user: %v", err)
	}

	withinTTL, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("within-TTL validation: %v", err)
	}
	if withinTTL.User.FirstName != "Cache" {
		t.Fatalf("within-TTL first name = %q, want cached value", withinTTL.User.FirstName)
	}

	manager.sessionValidation.ttl = 0
	afterTTL, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("after-TTL validation: %v", err)
	}
	if afterTTL.User.FirstName != "Updated" {
		t.Fatalf("after-TTL first name = %q, want Updated", afterTTL.User.FirstName)
	}
	stats := manager.SessionValidationCacheStats()
	if stats.DatabaseLoads != 2 || stats.StaleRejections != 1 {
		t.Fatalf("unexpected post-TTL cache stats: %+v", stats)
	}
}

func TestSessionValidationMutationInvalidatesCache(t *testing.T) {
	_, manager, session := newSessionValidationTestManager(t, time.Hour)

	if _, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP); err != nil {
		t.Fatalf("populate cache: %v", err)
	}
	if err := manager.SetAuthPending(session.ID, AuthPendingEnrollment); err != nil {
		t.Fatalf("SetAuthPending: %v", err)
	}
	restricted, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("validate restricted session: %v", err)
	}
	if !restricted.EnrollmentRequired || restricted.AuthPendingType != AuthPendingEnrollment {
		t.Fatalf("stale pending state: required=%v type=%q", restricted.EnrollmentRequired, restricted.AuthPendingType)
	}

	if err := manager.DeleteSession(session.Token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("validation after deletion = %v, want ErrSessionNotFound", err)
	}
	stats := manager.SessionValidationCacheStats()
	if stats.InvalidatedEntries < 2 {
		t.Fatalf("invalidated entries = %d, want at least 2", stats.InvalidatedEntries)
	}
}

func TestSessionValidationUserInvalidationRejectsDeactivatedUser(t *testing.T) {
	db, manager, session := newSessionValidationTestManager(t, time.Hour)

	if _, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP); err != nil {
		t.Fatalf("populate cache: %v", err)
	}
	if _, err := db.ExecWrite(`UPDATE users SET is_active = false WHERE id = ?`, session.UserID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	manager.InvalidateUserSessionValidation(session.UserID)

	if _, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("deactivated-user validation = %v, want ErrInvalidSession", err)
	}
}

func TestSessionValidationUserInvalidationKeepsUnrelatedSessionWarm(t *testing.T) {
	db, manager, first := newSessionValidationTestManager(t, time.Hour)
	if _, err := db.ExecWrite(`UPDATE users SET email_verified = false WHERE id = ?`, first.UserID); err != nil {
		t.Fatalf("mark first user unverified: %v", err)
	}

	result, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active, email_verified, created_at, updated_at)
		VALUES ('second-cache@example.com', 'second-cache', 'Second', 'Cache', true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("insert second user: %v", err)
	}
	secondUserID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("second user LastInsertId: %v", err)
	}
	second, err := manager.CreateSession(int(secondUserID), sessionValidationTestIP, "second-agent", false)
	if err != nil {
		t.Fatalf("CreateSession(second): %v", err)
	}

	firstCached, err := manager.ValidateSessionContext(context.Background(), first.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("prime first session: %v", err)
	}
	if firstCached.User.EmailVerified {
		t.Fatal("first cached user unexpectedly verified")
	}
	if _, err := manager.ValidateSessionContext(context.Background(), second.Token, sessionValidationTestIP); err != nil {
		t.Fatalf("prime second session: %v", err)
	}
	loadsBefore := manager.SessionValidationCacheStats().DatabaseLoads

	if _, err := db.ExecWrite(`UPDATE users SET email_verified = true WHERE id = ?`, first.UserID); err != nil {
		t.Fatalf("verify first user: %v", err)
	}
	manager.InvalidateUserSessionValidation(first.UserID)

	if _, err := manager.ValidateSessionContext(context.Background(), second.Token, sessionValidationTestIP); err != nil {
		t.Fatalf("validate unrelated session: %v", err)
	}
	if loads := manager.SessionValidationCacheStats().DatabaseLoads; loads != loadsBefore {
		t.Fatalf("unrelated validation database loads = %d, want %d", loads, loadsBefore)
	}

	firstReloaded, err := manager.ValidateSessionContext(context.Background(), first.Token, sessionValidationTestIP)
	if err != nil {
		t.Fatalf("reload verified first session: %v", err)
	}
	if !firstReloaded.User.EmailVerified {
		t.Fatal("first session retained stale email verification state")
	}
	if loads := manager.SessionValidationCacheStats().DatabaseLoads; loads != loadsBefore+1 {
		t.Fatalf("targeted validation database loads = %d, want %d", loads, loadsBefore+1)
	}
}

func TestSessionValidationTokenInvalidationKeepsUnrelatedSessionWarm(t *testing.T) {
	db, manager, first := newSessionValidationTestManager(t, time.Hour)
	result, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active, email_verified, created_at, updated_at)
		VALUES ('refresh-peer@example.com', 'refresh-peer', 'Refresh', 'Peer', true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("insert peer user: %v", err)
	}
	peerUserID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("peer LastInsertId: %v", err)
	}
	peer, err := manager.CreateSession(int(peerUserID), sessionValidationTestIP, "peer-agent", false)
	if err != nil {
		t.Fatalf("CreateSession(peer): %v", err)
	}

	for _, session := range []*Session{first, peer} {
		if _, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP); err != nil {
			t.Fatalf("prime session %d: %v", session.ID, err)
		}
	}
	loadsBefore := manager.SessionValidationCacheStats().DatabaseLoads
	if err := manager.RefreshSession(first.Token, false); err != nil {
		t.Fatalf("RefreshSession(first): %v", err)
	}
	if _, err := manager.ValidateSessionContext(context.Background(), peer.Token, sessionValidationTestIP); err != nil {
		t.Fatalf("validate peer after refresh: %v", err)
	}
	if loads := manager.SessionValidationCacheStats().DatabaseLoads; loads != loadsBefore {
		t.Fatalf("peer validation database loads = %d, want %d", loads, loadsBefore)
	}
}

func TestSessionValidationCacheCanBeDisabled(t *testing.T) {
	_, manager, session := newSessionValidationTestManager(t, 0)

	for range 2 {
		if _, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP); err != nil {
			t.Fatalf("ValidateSessionContext: %v", err)
		}
	}
	stats := manager.SessionValidationCacheStats()
	if stats.Enabled || stats.Entries != 0 || stats.DatabaseLoads != 2 {
		t.Fatalf("unexpected disabled-cache stats: %+v", stats)
	}
}

func TestNamedSessionValidationCachesHaveIndependentDiagnosticsBudgets(t *testing.T) {
	mainManager := NewSessionManagerWithNamedValidationCacheTTL(
		nil, false, false, nil, "main-secret", "strict", time.Minute, "session_validation", 4,
	)
	sshManager := NewSessionManagerWithNamedValidationCacheTTL(
		nil, false, false, nil, "ssh-secret", "strict", time.Minute, "ssh_session_validation", 4,
	)
	t.Cleanup(func() {
		_ = mainManager.sessionValidation.cache.Close()
		_ = sshManager.sessionValidation.cache.Close()
	})

	wantMaximum := int64(4 * 1024 * 1024)
	found := map[string]cacheutil.Snapshot{}
	for _, snapshot := range cacheutil.Snapshots() {
		if snapshot.Name == "session_validation" || snapshot.Name == "ssh_session_validation" {
			found[snapshot.Name] = snapshot
		}
	}
	for _, name := range []string{"session_validation", "ssh_session_validation"} {
		snapshot, ok := found[name]
		if !ok {
			t.Fatalf("%s cache missing from diagnostics: %+v", name, found)
		}
		if snapshot.MaximumCapacityBytes != wantMaximum {
			t.Fatalf("%s maximum = %d, want %d", name, snapshot.MaximumCapacityBytes, wantMaximum)
		}
	}
}

type gatedQueryDatabase struct {
	database.Database
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (db *gatedQueryDatabase) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	db.calls.Add(1)
	db.once.Do(func() { close(db.entered) })
	select {
	case <-db.release:
	case <-ctx.Done():
	}
	return db.Database.QueryRowContext(ctx, query, args...)
}

func TestSessionValidationSingleflightCoalescesConcurrentMisses(t *testing.T) {
	baseDB, _, session := newSessionValidationTestManager(t, time.Minute)
	gatedDB := &gatedQueryDatabase{
		Database: baseDB,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	manager := NewSessionManagerWithValidationCacheTTL(
		gatedDB,
		false,
		false,
		nil,
		"test-cookie-secret",
		"strict",
		time.Minute,
	)

	const requestCount = 64
	start := make(chan struct{})
	errorsChannel := make(chan error, requestCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)
	for range requestCount {
		go func() {
			defer waitGroup.Done()
			<-start
			_, err := manager.ValidateSessionContext(context.Background(), session.Token, sessionValidationTestIP)
			errorsChannel <- err
		}()
	}
	close(start)
	<-gatedDB.entered
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for manager.SessionValidationCacheStats().Misses < requestCount {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("validation misses = %d, want %d callers at the shared load", manager.SessionValidationCacheStats().Misses, requestCount)
		}
	}
	close(gatedDB.release)
	waitGroup.Wait()
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent validation: %v", err)
		}
	}
	if calls := gatedDB.calls.Load(); calls != 1 {
		t.Fatalf("QueryRowContext calls = %d, want 1", calls)
	}
	stats := manager.SessionValidationCacheStats()
	if stats.DatabaseLoads != 1 || stats.CoalescedWaiters == 0 {
		t.Fatalf("unexpected single-flight stats: %+v", stats)
	}
}
