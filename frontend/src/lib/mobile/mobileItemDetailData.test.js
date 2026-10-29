import { beforeEach, describe, expect, it, vi } from 'vitest';
import { items } from '../api/items.js';
import { hierarchyLevels, itemTypes } from '../api/configuration.js';
import { itemSCMLinks } from '../api/scm.js';




vi.mock('../api/items.js', () => ({
  items: {
        getDetailSummary: vi.fn(),
        get: vi.fn(),
        getAvailableStatusTransitions: vi.fn(),
        getWatchStatus: vi.fn(),
        getPersonalTasks: vi.fn(),
        getChildren: vi.fn(),
        getAncestors: vi.fn(),
      },
}))

vi.mock('../api/configuration.js', () => ({
  itemTypes: { getAll: vi.fn() },
  hierarchyLevels: { getAll: vi.fn() },
}))

vi.mock('../api/scm.js', () => ({
  itemSCMLinks: { getConnectionStatus: vi.fn() },
}));


const { loadMobileItemDetailSummary } = await import('./mobileItemDetailData.js');

describe('mobile item detail request graph', () => {
  beforeEach(() => vi.clearAllMocks());

  it('loads the cold above-the-fold graph with one mobile summary request', async () => {
    const response = { item: { id: 42 }, children: [] };
    items.getDetailSummary.mockResolvedValue(response);

    await expect(loadMobileItemDetailSummary(42)).resolves.toBe(response);

    expect(items.getDetailSummary).toHaveBeenCalledOnce();
    expect(items.getDetailSummary).toHaveBeenCalledWith(42, { surface: 'mobile' });
    expect(items.get).not.toHaveBeenCalled();
    expect(items.getAvailableStatusTransitions).not.toHaveBeenCalled();
    expect(items.getWatchStatus).not.toHaveBeenCalled();
    expect(items.getPersonalTasks).not.toHaveBeenCalled();
    expect(items.getChildren).not.toHaveBeenCalled();
    expect(items.getAncestors).not.toHaveBeenCalled();
    expect(itemTypes.getAll).not.toHaveBeenCalled();
    expect(hierarchyLevels.getAll).not.toHaveBeenCalled();
    expect(itemSCMLinks.getConnectionStatus).not.toHaveBeenCalled();
  });
});
