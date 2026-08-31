import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    items: {
      getDetailSummary: vi.fn(),
      get: vi.fn(),
      getAvailableStatusTransitions: vi.fn(),
      getWatchStatus: vi.fn(),
      getPersonalTasks: vi.fn(),
      getChildren: vi.fn(),
      getAncestors: vi.fn(),
    },
    itemTypes: { getAll: vi.fn() },
    hierarchyLevels: { getAll: vi.fn() },
    itemSCMLinks: { getConnectionStatus: vi.fn() },
  },
}));

const { api } = await import('../api.js');
const { loadMobileItemDetailSummary } = await import('./mobileItemDetailData.js');

describe('mobile item detail request graph', () => {
  beforeEach(() => vi.clearAllMocks());

  it('loads the cold above-the-fold graph with one mobile summary request', async () => {
    const response = { item: { id: 42 }, children: [] };
    api.items.getDetailSummary.mockResolvedValue(response);

    await expect(loadMobileItemDetailSummary(42)).resolves.toBe(response);

    expect(api.items.getDetailSummary).toHaveBeenCalledOnce();
    expect(api.items.getDetailSummary).toHaveBeenCalledWith(42, { surface: 'mobile' });
    expect(api.items.get).not.toHaveBeenCalled();
    expect(api.items.getAvailableStatusTransitions).not.toHaveBeenCalled();
    expect(api.items.getWatchStatus).not.toHaveBeenCalled();
    expect(api.items.getPersonalTasks).not.toHaveBeenCalled();
    expect(api.items.getChildren).not.toHaveBeenCalled();
    expect(api.items.getAncestors).not.toHaveBeenCalled();
    expect(api.itemTypes.getAll).not.toHaveBeenCalled();
    expect(api.hierarchyLevels.getAll).not.toHaveBeenCalled();
    expect(api.itemSCMLinks.getConnectionStatus).not.toHaveBeenCalled();
  });
});
