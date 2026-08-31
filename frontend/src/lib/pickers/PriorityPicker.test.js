import { render, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    workspaces: { get: vi.fn() },
    configurationSets: { get: vi.fn() },
    priorities: { getAll: vi.fn() },
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

const { api } = await import('../api.js');
const { default: PriorityPicker } = await import('./PriorityPicker.svelte');

describe('PriorityPicker', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads default priorities when the workspace configuration set has none', async () => {
    api.workspaces.get.mockResolvedValue({ id: 9, configuration_set_id: 12 });
    api.configurationSets.get.mockResolvedValue({ id: 12, priorities_detailed: [] });
    api.priorities.getAll.mockResolvedValue([{ id: 3, name: 'Medium', sort_order: 0 }]);

    render(PriorityPicker, { props: { workspaceId: 9 } });

    await waitFor(() => {
      expect(api.configurationSets.get).toHaveBeenCalledWith(12);
      expect(api.priorities.getAll).toHaveBeenCalled();
    });
  });
});
