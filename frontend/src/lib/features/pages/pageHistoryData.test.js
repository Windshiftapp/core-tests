import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/pages.js', () => ({
  pages: { getHistory: vi.fn() },
}));

import { pages } from '../../api/pages.js';
import { loadPageHistory, normalizePageHistoryResponse, pageRevisionAuthorName } from './pageHistoryData.js';

describe('page history request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads revisions and embedded author summaries with one request', async () => {
    pages.getHistory.mockResolvedValue({
      revisions: [{ id: 1, created_by: 7, author: { id: 7, name: 'Page Author' } }],
    });

    const revisions = await loadPageHistory(4, 9, { limit: 50 });

    expect(pages.getHistory).toHaveBeenCalledWith(4, 9, { limit: 50 });
    expect(pageRevisionAuthorName(revisions[0])).toBe('Page Author');
  });

  it('accepts cookie, v1, and direct list response shapes', () => {
    expect(normalizePageHistoryResponse({ revisions: [{ id: 1 }] })).toEqual([{ id: 1 }]);
    expect(normalizePageHistoryResponse({ items: [{ id: 2 }] })).toEqual([{ id: 2 }]);
    expect(normalizePageHistoryResponse([{ id: 3 }])).toEqual([{ id: 3 }]);
    expect(pageRevisionAuthorName({ created_by: 8 })).toBe('#8');
  });
});
