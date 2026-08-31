import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

vi.mock('../utils/crossTabSync.js', () => ({
  notifyItemMutation: vi.fn(),
}));

const { fetchAPI } = await import('./core.js');
const { notifyItemMutation } = await import('../utils/crossTabSync.js');
const { items } = await import('./items.js');
const { iterations } = await import('./milestones.js');

describe('bulk operation API clients', () => {
  beforeEach(() => {
    fetchAPI.mockReset();
    notifyItemMutation.mockReset();
  });

  it('sends one atomic request for a flexible work-item field patch', async () => {
    fetchAPI.mockResolvedValue({ updated_count: 100, items: [] });

    await items.bulkUpdate(
      Array.from({ length: 100 }, (_, index) => index + 1),
      { priority_id: 7, iteration_id: 12 }
    );

    expect(fetchAPI).toHaveBeenCalledTimes(1);
    expect(fetchAPI).toHaveBeenCalledWith('/items/bulk-update', {
      method: 'POST',
      body: JSON.stringify({
        item_ids: Array.from({ length: 100 }, (_, index) => index + 1),
        set: { priority_id: 7, iteration_id: 12 },
      }),
    });
    expect(notifyItemMutation).toHaveBeenCalledOnce();
  });

  it('sends distinct roadmap date patches in one atomic request', async () => {
    fetchAPI.mockResolvedValue({ updated_count: 2, items: [] });
    const patches = [
      { item_id: 11, set: { start_date: '2026-08-10' } },
      { item_id: 12, set: { end_date: '2026-08-20' } },
    ];

    await items.bulkPatch(patches);

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/items/bulk-patch', {
      method: 'POST',
      body: JSON.stringify({ patches }),
    });
    expect(notifyItemMutation).toHaveBeenCalledOnce();
  });

  it('loads the hierarchy date projection with one request', async () => {
    fetchAPI.mockResolvedValue({ items: [], truncated: false });

    await items.getRoadmapHierarchyDates([11, 12]);

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/items/roadmap-hierarchy-dates', {
      method: 'POST',
      body: JSON.stringify({ root_ids: [11, 12] }),
    });
    expect(notifyItemMutation).not.toHaveBeenCalled();
  });

  it('completes a 500-item iteration with one client request', async () => {
    fetchAPI.mockResolvedValue({ moved_count: 500, items: [] });

    await iterations.complete(41, 42);

    expect(fetchAPI).toHaveBeenCalledTimes(1);
    expect(fetchAPI).toHaveBeenCalledWith('/iterations/41/complete', {
      method: 'POST',
      body: JSON.stringify({ move_incomplete_to_iteration_id: 42 }),
    });
  });
});
