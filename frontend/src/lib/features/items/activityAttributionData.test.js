import { describe, expect, it, vi } from 'vitest';

vi.mock('../../api/misc.js', () => ({
  getComments: vi.fn(),
}));
vi.mock('../../api/items.js', () => ({
  items: { getHistory: vi.fn() },
}));

import { items } from '../../api/items.js';
import { getComments } from '../../api/misc.js';
import { agentOwnerName, loadAttributedComments, loadAttributedItemHistory } from './activityAttributionData.js';

describe('item activity attribution request graph', () => {
  it('loads attributed comments without per-agent owner requests', async () => {
    getComments.mockResolvedValue({
      comments: [{ id: 1, agent_owner_name: 'Agent Owner' }],
    });

    const response = await loadAttributedComments(42, { limit: 25 });

    expect(getComments).toHaveBeenCalledWith(42, { limit: 25 });
    expect(agentOwnerName(response.comments[0])).toBe('Agent Owner');
  });

  it('loads attributed history without per-agent owner requests', async () => {
    items.getHistory.mockResolvedValue([{ id: 2, agent_owner_name: 'History Owner' }]);

    const history = await loadAttributedItemHistory(42);

    expect(items.getHistory).toHaveBeenCalledWith(42);
    expect(agentOwnerName(history[0])).toBe('History Owner');
  });
});
