//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/cacheutil"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// The typed cache must hand back the exact stored pointer: a round-trip through
// JSON would allocate a copy and is the per-check cost WI-1906 removes.
func TestPermissionSnapshotCacheReturnsStoredPointerWithoutDecode(t *testing.T) {
	service := &PermissionService{
		cache: cacheutil.NewTyped[*models.UserPermissionCache]("permission_pointer_test", cacheutil.TypedCacheOptions{
			TTL:    time.Minute,
			Shards: 4,
		}),
		ttl: time.Minute,
	}
	t.Cleanup(func() { _ = service.cache.Close() })

	stored := &models.UserPermissionCache{
		UserID:            1,
		IsSystemAdmin:     true,
		GlobalPermissions: map[string]bool{"system.admin": true},
		ExpiresAt:         time.Now().Add(time.Minute),
	}
	if err := service.storeUserPermissionCache(1, stored); err != nil {
		t.Fatalf("storeUserPermissionCache: %v", err)
	}

	got, err := service.getUserPermissionCache(1)
	if err != nil {
		t.Fatalf("getUserPermissionCache: %v", err)
	}
	if got != stored {
		t.Fatalf("cached snapshot = %p, want the stored pointer %p (a decode copied it)", got, stored)
	}

	// A permission check served from the snapshot must not decode or
	// re-serialize anything.
	allowed, err := service.HasWorkspacePermission(1, 99, models.PermissionItemView)
	if err != nil {
		t.Fatalf("HasWorkspacePermission: %v", err)
	}
	if !allowed {
		t.Fatal("system admin was denied item.view")
	}
	if decodes := service.GetCacheStats().PermissionSnapshotDecodes; decodes != 0 {
		t.Fatalf("permission snapshot decodes = %d, want 0", decodes)
	}
}

func TestGetGroupMembershipsReturnsACopy(t *testing.T) {
	service := &PermissionService{
		cache: cacheutil.NewTyped[*models.UserPermissionCache]("permission_group_copy_test", cacheutil.TypedCacheOptions{
			TTL:    time.Minute,
			Shards: 4,
		}),
		ttl: time.Minute,
	}
	t.Cleanup(func() { _ = service.cache.Close() })

	if err := service.storeUserPermissionCache(1, &models.UserPermissionCache{
		UserID:           1,
		GroupMemberships: []int{10, 20},
		ExpiresAt:        time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("storeUserPermissionCache: %v", err)
	}

	memberships, err := service.GetGroupMemberships(1)
	if err != nil {
		t.Fatalf("GetGroupMemberships: %v", err)
	}
	if len(memberships) != 2 || memberships[0] != 10 || memberships[1] != 20 {
		t.Fatalf("memberships = %v, want [10 20]", memberships)
	}
	memberships[0] = 999

	again, err := service.GetGroupMemberships(1)
	if err != nil {
		t.Fatalf("GetGroupMemberships second call: %v", err)
	}
	if again[0] != 10 {
		t.Fatalf("mutating the returned slice changed the cached snapshot: %v", again)
	}
}

// insertAgent creates an owned agent row. Agents resolve their owner's
// permissions, which is the path that used to shallow-copy the owner snapshot.
func (e *permTestEnv) insertAgent(ownerID int, email string) int {
	e.tb.Helper()
	id := testutils.InsertID(e.tb, e.db, 
		`INSERT INTO users (email, username, first_name, last_name, is_agent, agent_owner_user_id) VALUES (?, ?, ?, '', true, ?)`,
		email, email, email, ownerID,
	)
	return int(id)
}

// Regression: GetGroupMemberships used to hand out the snapshot's own slice, so
// a caller that mutated it (or appended into its spare capacity) corrupted the
// cached snapshot and every later read observed the caller's edit. This drives
// the real build path instead of seeding the cache by hand.
func TestGetGroupMembershipsBuildDoesNotLeakCallerMutations(t *testing.T) {
	env := newPermTestEnv(t)
	t.Cleanup(func() { _ = env.service.Close() })
	userID := env.insertUser("membership-copy@example.test")
	groupID := env.insertGroup("membership-copy", true)
	env.addGroupMember(groupID, userID)

	memberships, err := env.service.GetGroupMemberships(userID)
	if err != nil {
		t.Fatalf("GetGroupMemberships: %v", err)
	}
	if len(memberships) != 1 || memberships[0] != groupID {
		t.Fatalf("memberships = %v, want [%d]", memberships, groupID)
	}

	memberships[0] = 999
	memberships = append(memberships, 1000)

	again, err := env.service.GetGroupMemberships(userID)
	if err != nil {
		t.Fatalf("GetGroupMemberships second call: %v", err)
	}
	if len(again) != 1 || again[0] != groupID {
		t.Fatalf("caller mutation leaked into the cached snapshot: %v", again)
	}
}

// Regression: the agent snapshot must not share maps with the owner's snapshot.
// The typed cache stores live pointers, so a shared map would let a mutation
// through one user's snapshot change another user's cached permissions.
func TestAgentSnapshotDoesNotAliasOwnerSnapshot(t *testing.T) {
	env := newPermTestEnv(t)
	t.Cleanup(func() { _ = env.service.Close() })
	ownerID := env.insertUser("agent-owner@example.test")
	agentID := env.insertAgent(ownerID, "agent-owner-agent@example.test")
	workspaceID := env.insertWorkspace("agent-workspace")
	env.assignUserWorkspaceRole(ownerID, workspaceID, env.roleID("Viewer"))

	agentSnapshot, err := env.service.GetUserEffectivePermissions(agentID)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions(agent): %v", err)
	}
	if agentSnapshot.UserID != agentID {
		t.Fatalf("agent snapshot user = %d, want %d", agentSnapshot.UserID, agentID)
	}
	ownerSnapshot, err := env.service.GetUserEffectivePermissions(ownerID)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions(owner): %v", err)
	}
	if agentSnapshot == ownerSnapshot {
		t.Fatal("agent and owner share one snapshot pointer")
	}

	// The agent inherits the owner's grant, but the maps must be independent in
	// both directions.
	agentSnapshot.WorkspacePermissions[workspaceID][models.PermissionItemView] = false
	if !ownerSnapshot.WorkspacePermissions[workspaceID][models.PermissionItemView] {
		t.Fatal("agent snapshot mutation leaked into the owner snapshot")
	}
	ownerSnapshot.GlobalPermissions["leaked"] = true
	if agentSnapshot.GlobalPermissions["leaked"] {
		t.Fatal("owner snapshot mutation leaked into the agent snapshot")
	}
}

func TestPermissionCacheMaxEntries(t *testing.T) {
	if got := permissionCacheMaxEntries(PermissionCacheConfig{MaxEntries: 42, MaxCacheSize: 16}); got != 42 {
		t.Fatalf("explicit MaxEntries = %d, want 42", got)
	}
	if got := permissionCacheMaxEntries(PermissionCacheConfig{MaxCacheSize: 16}); got != 1024 {
		t.Fatalf("derived MaxEntries = %d, want 1024", got)
	}
	if got := permissionCacheMaxEntries(PermissionCacheConfig{}); got != 0 {
		t.Fatalf("unconfigured MaxEntries = %d, want 0 (unbounded)", got)
	}
}

func TestCloneUserPermissionCacheDeepCopies(t *testing.T) {
	original := &models.UserPermissionCache{
		UserID:               1,
		IsSystemAdmin:        true,
		GlobalPermissions:    map[string]bool{"global": true},
		WorkspacePermissions: map[int]map[string]bool{1: {"ws": true}},
		WorkspaceEveryone:    map[int]map[string]bool{1: {"everyone": true}},
		GroupMemberships:     []int{10, 20},
		RoleAssignments:      map[int][]int{1: {5}},
		DirectPermissions:    map[int][]string{1: {"direct"}},
		PermissionSources:    map[int]map[string]string{1: {"ws": "role"}},
	}

	clone := cloneUserPermissionCache(original)

	clone.GlobalPermissions["global"] = false
	clone.WorkspacePermissions[1]["ws"] = false
	clone.WorkspaceEveryone[1]["everyone"] = false
	clone.GroupMemberships[0] = 999
	clone.RoleAssignments[1][0] = 999
	clone.DirectPermissions[1][0] = "changed"
	clone.PermissionSources[1]["ws"] = "changed"

	if !original.GlobalPermissions["global"] {
		t.Fatal("global permissions aliased the clone")
	}
	if !original.WorkspacePermissions[1]["ws"] {
		t.Fatal("workspace permissions aliased the clone")
	}
	if !original.WorkspaceEveryone[1]["everyone"] {
		t.Fatal("workspace everyone aliased the clone")
	}
	if original.GroupMemberships[0] != 10 {
		t.Fatal("group memberships aliased the clone")
	}
	if original.RoleAssignments[1][0] != 5 {
		t.Fatal("role assignments aliased the clone")
	}
	if original.DirectPermissions[1][0] != "direct" {
		t.Fatal("direct permissions aliased the clone")
	}
	if original.PermissionSources[1]["ws"] != "role" {
		t.Fatal("permission sources aliased the clone")
	}
}
