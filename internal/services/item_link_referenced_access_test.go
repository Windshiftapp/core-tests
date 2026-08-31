package services

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

type countingWorkspacePermissions struct {
	allowed map[int]bool
	calls   map[int]int
}

func (p *countingWorkspacePermissions) HasWorkspacePermission(_ int, workspaceID int, _ string) (bool, error) {
	p.calls[workspaceID]++
	return p.allowed[workspaceID], nil
}

func (p *countingWorkspacePermissions) AccessibleWorkspaceIDs(int) ([]int, error) {
	return nil, nil
}

func (p *countingWorkspacePermissions) AccessibleWorkspaceIDKeys(int) ([]repository.IDKey, error) {
	return nil, nil
}

type countingAssetPermissions struct{ calls int }

func (p *countingAssetPermissions) HasAssetSetPermission(int, int, string) (bool, error) {
	p.calls++
	return true, nil
}

func intPointer(value int) *int { return &value }

func TestAuthorizeReferencedLinkScopesItemOnlySkipsAssetWork(t *testing.T) {
	workspacePerms := &countingWorkspacePermissions{
		allowed: map[int]bool{10: true, 20: true},
		calls:   map[int]int{},
	}
	assetPerms := &countingAssetPermissions{}
	service := &ItemLinkService{perm: workspacePerms, assetPerm: assetPerms}
	links := []models.ItemLink{
		{
			SourceType: "item", SourceID: 1, SourceWorkspaceID: intPointer(10), SourceWorkspaceKey: "A",
			TargetType: "item", TargetID: 2, TargetWorkspaceID: intPointer(20), TargetWorkspaceKey: "B",
		},
		{
			SourceType: "item", SourceID: 3, SourceWorkspaceID: intPointer(10), SourceWorkspaceKey: "A",
			TargetType: "item", TargetID: 4, TargetWorkspaceID: intPointer(20), TargetWorkspaceKey: "B",
		},
	}

	access := service.authorizeReferencedLinkScopes(99, links)
	visible := service.filterLinksByAccessWithScopes(
		links,
		access.workspaceKeys,
		access.workspaceIDs,
		access.testWorkspaceIDs,
		access.assetSetIDs,
		access.scopes,
	)

	if len(visible) != len(links) {
		t.Fatalf("visible links = %d, want %d", len(visible), len(links))
	}
	if workspacePerms.calls[10] != 1 || workspacePerms.calls[20] != 1 {
		t.Fatalf("workspace checks = %#v, want one per referenced workspace", workspacePerms.calls)
	}
	if assetPerms.calls != 0 {
		t.Fatalf("asset permission checks = %d, want 0", assetPerms.calls)
	}
	if access.scopeSQLQueries != 0 {
		t.Fatalf("scope SQL queries = %d, want 0 for item-only links", access.scopeSQLQueries)
	}
}

func TestAuthorizeReferencedLinkScopesFailsClosedPerWorkspace(t *testing.T) {
	workspacePerms := &countingWorkspacePermissions{
		allowed: map[int]bool{10: true, 20: false},
		calls:   map[int]int{},
	}
	service := &ItemLinkService{perm: workspacePerms}
	links := []models.ItemLink{{
		SourceType: "item", SourceID: 1, SourceWorkspaceID: intPointer(10), SourceWorkspaceKey: "A",
		TargetType: "item", TargetID: 2, TargetWorkspaceID: intPointer(20), TargetWorkspaceKey: "B",
	}}

	access := service.authorizeReferencedLinkScopes(99, links)
	visible := service.filterLinksByAccessWithScopes(
		links,
		access.workspaceKeys,
		access.workspaceIDs,
		access.testWorkspaceIDs,
		access.assetSetIDs,
		access.scopes,
	)
	if len(visible) != 0 {
		t.Fatalf("visible links = %d, want fail-closed filtering", len(visible))
	}
}

func BenchmarkReferencedLinkPermissionsSparseAmong1000Scopes(b *testing.B) {
	workspacePerms := &countingWorkspacePermissions{allowed: map[int]bool{1: true}, calls: map[int]int{}}
	assetPerms := &countingAssetPermissions{}
	service := &ItemLinkService{perm: workspacePerms, assetPerm: assetPerms}
	workspaceScopes := map[int]struct{}{1: {}}
	assetSetScopes := map[int]struct{}{1: {}}
	workspaceKeys := map[int]map[string]struct{}{1: {"W1": {}}}

	b.ResetTimer()
	for range b.N {
		access := referencedLinkAccess{
			workspaceKeys: map[string]bool{},
			workspaceIDs:  map[int]bool{},
			assetSetIDs:   map[int]bool{},
		}
		service.applyReferencedScopePermissions(99, workspaceScopes, nil, assetSetScopes, workspaceKeys, &access)
	}
}

func BenchmarkReferencedLinkPermissionsDense1000Scopes(b *testing.B) {
	allowed := make(map[int]bool, 1000)
	workspaceScopes := make(map[int]struct{}, 1000)
	assetSetScopes := make(map[int]struct{}, 1000)
	workspaceKeys := make(map[int]map[string]struct{}, 1000)
	for id := 1; id <= 1000; id++ {
		allowed[id] = true
		workspaceScopes[id] = struct{}{}
		assetSetScopes[id] = struct{}{}
		workspaceKeys[id] = map[string]struct{}{}
	}
	workspacePerms := &countingWorkspacePermissions{allowed: allowed, calls: map[int]int{}}
	assetPerms := &countingAssetPermissions{}
	service := &ItemLinkService{perm: workspacePerms, assetPerm: assetPerms}

	b.ResetTimer()
	for range b.N {
		access := referencedLinkAccess{
			workspaceKeys: map[string]bool{},
			workspaceIDs:  map[int]bool{},
			assetSetIDs:   map[int]bool{},
		}
		service.applyReferencedScopePermissions(99, workspaceScopes, nil, assetSetScopes, workspaceKeys, &access)
	}
}
