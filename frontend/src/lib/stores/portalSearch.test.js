import { beforeEach, describe, expect, it, vi } from 'vitest';
import { portal } from '../api/portal.js';

const mocks = vi.hoisted(() => ({ searchKnowledgeBase: vi.fn() }));

vi.mock('../api/portal.js', () => ({
  portal: { searchKnowledgeBase: mocks.searchKnowledgeBase },
}));

import { configurePortalSearchStore, portalSearchStore } from './portalSearch.svelte.js';

beforeEach(() => {
  vi.clearAllMocks();
  portalSearchStore.reset();
  configurePortalSearchStore({
    getKnowledgeBaseShareLink: () => 'https://docs.example.test/share/guide',
    getSlug: () => 'support',
  });
});

describe('portalSearchStore', () => {
  it('searches within the active portal context', async () => {
    mocks.searchKnowledgeBase.mockResolvedValue({ data: [{ id: 'doc-1' }] });
    portalSearchStore.query = 'release notes';

    await portalSearchStore.search();

    expect(mocks.searchKnowledgeBase).toHaveBeenCalledWith('support', 'release notes');
    expect(portalSearchStore.results).toEqual({ data: [{ id: 'doc-1' }] });
    expect(portalSearchStore.visible).toBe(true);
  });

  it('discards a delayed response after reset', async () => {
    let resolveSearch;
    mocks.searchKnowledgeBase.mockReturnValue(
      new Promise((resolve) => {
        resolveSearch = resolve;
      })
    );
    portalSearchStore.query = 'stale query';
    const pendingSearch = portalSearchStore.search();

    portalSearchStore.reset();
    resolveSearch({ data: [{ id: 'stale' }] });
    await pendingSearch;

    expect(portalSearchStore.query).toBe('');
    expect(portalSearchStore.results).toBeNull();
    expect(portalSearchStore.loading).toBe(false);
  });
});
