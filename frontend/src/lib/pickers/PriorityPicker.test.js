import { render, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaces } from '../api/workspaces.js';
import { configurationSets, priorities } from '../api/configuration.js';



vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        get: vi.fn(),
        // WI-1351: workspace-scoped priority configuration resolves through
        // workspaceDataStore.screenConfig, which calls getEffectiveConfig.
        getEffectiveConfig: vi.fn(),
      },
}))

vi.mock('../api/configuration.js', () => ({
  configurationSets: { get: vi.fn() },
  priorities: { getAll: vi.fn() },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));


const { default: PriorityPicker } = await import('./PriorityPicker.svelte');

describe('PriorityPicker', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads default priorities when the workspace configuration set has none', async () => {
    // Store is uninitialized in this test, so screenConfig resolves to null
    // without hitting the API, and the picker falls back to the global
    // priority list.
    workspaces.getEffectiveConfig.mockResolvedValue({ priorities: [] });
    priorities.getAll.mockResolvedValue([{ id: 3, name: 'Medium', sort_order: 0 }]);

    render(PriorityPicker, { props: { workspaceId: 9 } });

    await waitFor(() => {
      expect(priorities.getAll).toHaveBeenCalled();
    });
  });
});
