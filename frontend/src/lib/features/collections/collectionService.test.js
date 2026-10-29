import { beforeEach, describe, expect, it, vi } from 'vitest';
import { items } from '../../api/items.js';
import { collections } from '../../api/collections.js';



vi.mock('../../api/items.js', () => ({
  items: { getAll: vi.fn(), getBacklog: vi.fn() },
}))

vi.mock('../../api/collections.js', () => ({
  collections: { get: vi.fn() },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));


const {
  fetchCollectionItems,
  fetchCollectionBacklog,
  buildCollectionAssociationUpdate,
  canConfigureListColumns,
  checkItemVisibility,
  resolveCreatedItemMembership,
  applyCollectionPrivacyToggle,
} = await import('./collectionService.js');

beforeEach(() => {
  vi.resetAllMocks();
  collections.get.mockResolvedValue({ name: 'Everything', ql_query: '' });
});

describe('canConfigureListColumns', () => {
  it('lets a collection owner without workspace admin configure collection columns', () => {
    expect(
      canConfigureListColumns({
        collectionId: 4,
        collectionOwnerId: 9,
        currentUserId: 9,
        canAdminWorkspace: false,
      })
    ).toBe(true);
  });

  it('does not let a non-owner workspace admin configure collection columns', () => {
    expect(
      canConfigureListColumns({
        collectionId: 4,
        collectionOwnerId: 9,
        currentUserId: 2,
        canAdminWorkspace: true,
      })
    ).toBe(false);
  });

  it('lets a global collection owner configure columns without a workspace', () => {
    expect(
      canConfigureListColumns({
        collectionId: 4,
        collectionOwnerId: 9,
        currentUserId: 9,
        canAdminWorkspace: false,
      })
    ).toBe(true);
  });

  it('gates workspace-default columns on workspace administration', () => {
    expect(
      canConfigureListColumns({
        collectionId: null,
        collectionOwnerId: null,
        currentUserId: 2,
        canAdminWorkspace: true,
      })
    ).toBe(true);
    expect(
      canConfigureListColumns({
        collectionId: null,
        collectionOwnerId: null,
        currentUserId: 2,
        canAdminWorkspace: false,
      })
    ).toBe(false);
  });
});

describe('buildCollectionAssociationUpdate', () => {
  const collection = {
    id: 1,
    name: 'Scoped',
    description: 'Desc',
    category_id: 7,
    is_private: true,
  };

  it('omits public-sharing fields so association does not require publishing permission', () => {
    const payload = buildCollectionAssociationUpdate({
      collection,
      qlQuery: 'status_id = 1',
      filterState: null,
      workspaceId: 3,
    });
    expect(payload).not.toHaveProperty('is_public');
    expect(payload).not.toHaveProperty('public_slug');
    expect(payload).toEqual({
      name: 'Scoped',
      description: 'Desc',
      ql_query: 'status_id = 1',
      filter_state: null,
      workspace_id: 3,
      category_id: null,
      is_private: true,
    });
  });

  it('preserves the category when moving back to the global scope', () => {
    const payload = buildCollectionAssociationUpdate({
      collection,
      qlQuery: 'status_id = 1',
      filterState: null,
      workspaceId: null,
    });
    expect(payload.workspace_id).toBeNull();
    expect(payload.category_id).toBe(7);
  });
});

describe('checkItemVisibility membership classification', () => {
  it('returns member when the item is in the filtered result', async () => {
    items.getAll.mockResolvedValue({ data: [{ id: 5 }] });
    await expect(checkItemVisibility(5, { collection_id: 1 })).resolves.toBe('member');
  });

  it('returns nonmember when the item is absent', async () => {
    items.getAll.mockResolvedValue({ data: [{ id: 6 }] });
    await expect(checkItemVisibility(5, { collection_id: 1 })).resolves.toBe('nonmember');
  });

  it('returns unknown instead of assuming membership after a 429', async () => {
    items.getAll.mockRejectedValue(
      Object.assign(new Error('too many concurrent requests'), { status: 429 })
    );
    await expect(checkItemVisibility(5, { collection_id: 1 })).resolves.toBe('unknown');
  });
});

describe('resolveCreatedItemMembership', () => {
  it('trusts workspace equality only when no collection or sub-filter applies', async () => {
    await expect(
      resolveCreatedItemMembership({ id: 5, workspace_id: 1 }, { workspaceId: 1 })
    ).resolves.toBe('member');
    await expect(
      resolveCreatedItemMembership({ id: 5, workspace_id: 2 }, { workspaceId: 1 })
    ).resolves.toBe('nonmember');
    expect(items.getAll).not.toHaveBeenCalled();
  });

  it('verifies collection QL membership server-side instead of trusting the workspace', async () => {
    items.getAll.mockResolvedValue({ data: [] });

    await expect(
      resolveCreatedItemMembership(
        { id: 5, workspace_id: 1 },
        { workspaceId: 1, collectionId: 9 }
      )
    ).resolves.toBe('nonmember');
    expect(items.getAll).toHaveBeenCalledWith({ collection_id: 9, id: 5 });
  });

  it('verifies an active sub-filter even in a workspace-scoped view', async () => {
    items.getAll.mockResolvedValue({ data: [{ id: 5 }] });

    await expect(
      resolveCreatedItemMembership(
        { id: 5, workspace_id: 1 },
        { workspaceId: 1, subFilterQL: 'status_completed = false' }
      )
    ).resolves.toBe('member');
    expect(items.getAll).toHaveBeenCalledWith({
      workspace_id: 1,
      sub_ql: 'status_completed = false',
      id: 5,
    });
  });

  it('surfaces unknown instead of inserting when verification fails', async () => {
    items.getAll.mockRejectedValue(new Error('offline'));

    await expect(
      resolveCreatedItemMembership(
        { id: 5, workspace_id: 1 },
        { workspaceId: 1, collectionId: 9 }
      )
    ).resolves.toBe('unknown');
  });
});

describe('applyCollectionPrivacyToggle', () => {
  it('clears public sharing when a collection becomes private', () => {
    const current = {
      id: 7,
      name: 'Board',
      is_private: false,
      is_public: true,
      public_slug: 'board',
    };
    // The API omits public_slug once cleared, so the merge must reset it from
    // the response rather than preserving the previous slug.
    const merged = applyCollectionPrivacyToggle(current, { is_public: false }, true);

    expect(merged).toMatchObject({
      id: 7,
      name: 'Board',
      is_private: true,
      is_public: false,
      public_slug: null,
    });
  });

  it('keeps the published board when a collection becomes shared', () => {
    const current = { id: 7, is_private: true, is_public: false, public_slug: null };
    const merged = applyCollectionPrivacyToggle(
      current,
      { is_public: true, public_slug: 'board' },
      false
    );

    expect(merged).toMatchObject({ is_private: false, is_public: true, public_slug: 'board' });
  });
});

describe('collection pagination contract', () => {
  it.each([
    ['items', fetchCollectionItems, 'getAll'],
    ['backlog', fetchCollectionBacklog, 'getBacklog'],
  ])('preserves the server page size for %s continuation', async (_name, fetch, method) => {
    items[method].mockResolvedValue({
      data: [{ id: 51 }],
      pagination: { page: 2, page_size: 50, total_items: 51, total_pages: 2 },
    });
    const result = await fetch(null, 7, { page: 2, limit: 50 });
    expect(result.items).toEqual([{ id: 51 }]);
    expect(result.pagination).toEqual({
      page: 2,
      page_size: 50,
      limit: 50,
      total_items: 51,
      total_pages: 2,
    });
  });
});
