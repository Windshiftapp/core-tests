import { describe, expect, it, vi } from 'vitest';
import {
  agentOwnerName,
  loadAttributedComments,
  loadAttributedItemHistory,
} from './activityAttributionData.js';

describe('item activity attribution request graph', () => {
  it('loads attributed comments without per-agent owner requests', async () => {
    const apiClient = {
      getComments: vi.fn().mockResolvedValue({
        comments: [{ id: 1, agent_owner_name: 'Agent Owner' }],
      }),
      getAgentOwner: vi.fn(),
    };

    const response = await loadAttributedComments(apiClient, 42, { limit: 25 });

    expect(apiClient.getComments).toHaveBeenCalledWith(42, { limit: 25 });
    expect(apiClient.getAgentOwner).not.toHaveBeenCalled();
    expect(agentOwnerName(response.comments[0])).toBe('Agent Owner');
  });

  it('loads attributed history without per-agent owner requests', async () => {
    const apiClient = {
      items: {
        getHistory: vi.fn().mockResolvedValue([{ id: 2, agent_owner_name: 'History Owner' }]),
      },
      getAgentOwner: vi.fn(),
    };

    const history = await loadAttributedItemHistory(apiClient, 42);

    expect(apiClient.items.getHistory).toHaveBeenCalledWith(42);
    expect(apiClient.getAgentOwner).not.toHaveBeenCalled();
    expect(agentOwnerName(history[0])).toBe('History Owner');
  });
});
