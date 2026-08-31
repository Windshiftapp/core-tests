import { get } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    workspaces: {
      getHomepageLayout: vi.fn(),
    },
  },
}));

vi.mock('./workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
    workspaceId: 12,
    initialized: true,
    homepageLayout: { gradient: 3, applyToAllViews: true },
    initialize: vi.fn(),
    hydrateHomepageLayout: vi.fn(),
  },
}));

import { api } from '../api.js';
import { workspaceDataStore } from './workspaceDataStore.svelte.js';
import {
  applyToAllViews,
  clearWorkspaceGradient,
  loadWorkspaceGradient,
  useGradientStyles,
  workspaceGradientIndex,
} from './workspaceGradient.svelte.js';

describe('workspace gradient loading', () => {
  beforeEach(() => {
    clearWorkspaceGradient();
    vi.clearAllMocks();
    workspaceDataStore.initialize.mockReset().mockResolvedValue();
    api.workspaces.getHomepageLayout.mockReset();
  });

  it('single-flights and caches a workspace layout', async () => {
    let resolveInitialization;
    workspaceDataStore.initialize.mockReturnValue(
      new Promise((resolve) => {
        resolveInitialization = resolve;
      })
    );

    const first = loadWorkspaceGradient(12);
    const second = loadWorkspaceGradient('12');

    expect(workspaceDataStore.initialize).toHaveBeenCalledTimes(1);
    resolveInitialization();
    await Promise.all([first, second]);

    await loadWorkspaceGradient(12);
    expect(workspaceDataStore.initialize).toHaveBeenCalledTimes(1);
    expect(api.workspaces.getHomepageLayout).not.toHaveBeenCalled();
    expect(get(workspaceGradientIndex)).toBe(3);
  });

  it('can explicitly refresh the layout outside the cached bootstrap snapshot', async () => {
    api.workspaces.getHomepageLayout.mockResolvedValue({
      gradient: 4,
      applyToAllViews: false,
    });

    await loadWorkspaceGradient(12, { force: true });

    expect(api.workspaces.getHomepageLayout).toHaveBeenCalledWith(12);
    expect(workspaceDataStore.hydrateHomepageLayout).toHaveBeenCalledWith(
      12,
      expect.objectContaining({ gradient: 4 })
    );
    expect(get(workspaceGradientIndex)).toBe(4);
  });
});

describe('workspace gradient styles', () => {
  beforeEach(() => {
    clearWorkspaceGradient();
  });

  it('keeps the raised card surface while allowing backdrop blur to be disabled', () => {
    workspaceGradientIndex.set(3);
    applyToAllViews.set(true);
    const styles = useGradientStyles();

    expect(styles.cardStyle(0)).toBe(
      'background-color: var(--ds-surface-raised); border-color: var(--ds-glass-border);'
    );
    expect(styles.columnStyle(12)).toContain('backdrop-filter: blur(12px);');
  });
});
