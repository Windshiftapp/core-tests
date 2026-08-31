import { describe, expect, it, vi } from 'vitest';
import {
  loadPageHistory,
  normalizePageHistoryResponse,
  pageRevisionAuthorName,
} from './pageHistoryData.js';

describe('page history request graph', () => {
  it('loads revisions and embedded author summaries with one request', async () => {
    const apiClient = {
      pages: {
        getHistory: vi.fn().mockResolvedValue({
          revisions: [{ id: 1, created_by: 7, author: { id: 7, name: 'Page Author' } }],
        }),
      },
      getUser: vi.fn(),
    };

    const revisions = await loadPageHistory(apiClient, 4, 9, { limit: 50 });

    expect(apiClient.pages.getHistory).toHaveBeenCalledWith(4, 9, { limit: 50 });
    expect(apiClient.getUser).not.toHaveBeenCalled();
    expect(pageRevisionAuthorName(revisions[0])).toBe('Page Author');
  });

  it('accepts cookie, v1, and direct list response shapes', () => {
    expect(normalizePageHistoryResponse({ revisions: [{ id: 1 }] })).toEqual([{ id: 1 }]);
    expect(normalizePageHistoryResponse({ items: [{ id: 2 }] })).toEqual([{ id: 2 }]);
    expect(normalizePageHistoryResponse([{ id: 3 }])).toEqual([{ id: 3 }]);
    expect(pageRevisionAuthorName({ created_by: 8 })).toBe('#8');
  });
});
