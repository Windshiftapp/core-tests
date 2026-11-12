import { beforeEach, describe, expect, it, vi } from 'vitest';

// The module only needs the event name from the API layer; mocking keeps the
// test off the whole request stack.
vi.mock('../api/core.js', () => ({
  API_MUTATION_EVENT: 'windshift:api-mutation',
}));

// The real stores are the runtime defaults; the test injects fakes instead.
vi.mock('./workspaceDataStore.svelte.js', () => ({ workspaceDataStore: {} }));
vi.mock('./collectionEditorOptions.svelte.js', () => ({ collectionEditorOptions: {} }));

const { referenceRulesForEndpoint, applyReferenceInvalidation } = await import(
  './referenceDataInvalidation.js'
);

function fakeCaches(workspaceId = 5) {
  return {
    store: {
      workspaceId,
      invalidate: vi.fn().mockResolvedValue(undefined),
      invalidateScreenConfigs: vi.fn(),
      refresh: vi.fn().mockResolvedValue(undefined),
    },
    editorOptions: {
      invalidate: vi.fn(),
      invalidateFamily: vi.fn(),
      reset: vi.fn(),
    },
  };
}

describe('referenceRulesForEndpoint', () => {
  it('ignores endpoints that are not reference data', () => {
    expect(referenceRulesForEndpoint('/items/12')).toEqual([]);
    expect(referenceRulesForEndpoint('/items/12/comments')).toEqual([]);
    expect(referenceRulesForEndpoint('/workspaces/3/collections/2/items')).toEqual([]);
    expect(referenceRulesForEndpoint('')).toEqual([]);
    expect(referenceRulesForEndpoint(undefined)).toEqual([]);
  });

  it('matches workspace-scoped and global reference endpoints', () => {
    expect(referenceRulesForEndpoint('/workspaces/5/milestones/9')).toHaveLength(1);
    expect(referenceRulesForEndpoint('/milestones')).toHaveLength(1);
    expect(referenceRulesForEndpoint('/workspaces/5/agent-profiles')).toHaveLength(1);
    expect(referenceRulesForEndpoint('/admin/groups/4')).toHaveLength(1);
    expect(referenceRulesForEndpoint('/time/projects/7')).toHaveLength(1);
  });
});

describe('applyReferenceInvalidation', () => {
  let caches;

  beforeEach(() => {
    caches = fakeCaches();
  });

  it('clears the scoped store field and editor family for a workspace mutation', () => {
    expect(applyReferenceInvalidation('/workspaces/5/milestones/9', caches)).toBe(true);
    expect(caches.store.invalidate).toHaveBeenCalledWith('milestones');
    expect(caches.editorOptions.invalidate).toHaveBeenCalledWith(5, 'milestones');
    expect(caches.editorOptions.invalidateFamily).not.toHaveBeenCalled();
  });

  it('clears every scoped editor cache for a global mutation', () => {
    expect(applyReferenceInvalidation('/milestones', caches)).toBe(true);
    expect(caches.store.invalidate).toHaveBeenCalledWith('milestones');
    expect(caches.editorOptions.invalidateFamily).toHaveBeenCalledWith('milestones');
    expect(caches.editorOptions.invalidate).not.toHaveBeenCalled();
  });

  it('refreshes the active workspace store even when the active scope differs', () => {
    caches.store.workspaceId = 7;
    applyReferenceInvalidation('/workspaces/5/iterations', caches);
    expect(caches.store.invalidate).toHaveBeenCalledWith('iterations');
    expect(caches.editorOptions.invalidate).toHaveBeenCalledWith(5, 'iterations');
  });

  it('drops effective screen configs for config mutations', () => {
    applyReferenceInvalidation('/workspaces/5/item-types/2', caches);
    expect(caches.store.invalidate).toHaveBeenCalledWith('itemTypes');
    expect(caches.store.invalidateScreenConfigs).toHaveBeenCalled();
  });

  it('refreshes the workspace record on a workspace update', () => {
    applyReferenceInvalidation('/workspaces/5', caches);
    expect(caches.store.invalidate).toHaveBeenCalledWith('workspace');
  });

  it('treats agent profiles as user-directory changes', () => {
    applyReferenceInvalidation('/workspaces/5/agent-profiles/8', caches);
    expect(caches.store.invalidate).toHaveBeenCalledWith('users');
    expect(caches.editorOptions.invalidate).toHaveBeenCalledWith(5, 'users');
  });

  it('does nothing for unrelated endpoints', () => {
    expect(applyReferenceInvalidation('/items/12', caches)).toBe(false);
    expect(caches.store.invalidate).not.toHaveBeenCalled();
    expect(caches.editorOptions.invalidate).not.toHaveBeenCalled();
    expect(caches.editorOptions.invalidateFamily).not.toHaveBeenCalled();
  });

  it('skips the workspace store while a global scope is active', () => {
    caches.store.workspaceId = 'global';
    applyReferenceInvalidation('/milestones', caches);
    expect(caches.store.invalidate).not.toHaveBeenCalled();
    expect(caches.editorOptions.invalidateFamily).toHaveBeenCalledWith('milestones');
  });

  it('falls back to a full refresh for cross-cutting changes', () => {
    applyReferenceInvalidation('/admin/object-translations/status/3/name/de', caches);
    expect(caches.store.refresh).toHaveBeenCalled();
    expect(caches.editorOptions.reset).toHaveBeenCalled();
  });
});
