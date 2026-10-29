import { beforeEach, describe, expect, it, vi } from 'vitest';
import { hub } from '../api/hub.js';

const mocks = vi.hoisted(() => ({
  hub: {
    get: vi.fn(),
    updateConfig: vi.fn(),
    getInbox: vi.fn(),
  },
  authenticated: true,
}));

vi.mock('../api/hub.js', () => ({
  hub: mocks.hub,
}));

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: {
      get isAuthenticated() {
        return mocks.authenticated;
      },
    },
}));

const { hubStore } = await import('./hub.svelte.js');

describe('hub section normalization', () => {
  beforeEach(() => {
    hubStore.reset();
    vi.clearAllMocks();
    mocks.authenticated = true;
  });

  it('defaults missing section fields from a legacy config', async () => {
    mocks.hub.get.mockResolvedValue({
      config: {
        sections: [{ id: 'a', title: 'Legacy', content: 'text', visible: true }],
      },
      portals: [{ id: 1, name: 'Portal' }],
      open_request_count: 0,
    });

    await hubStore.loadHub();

    const [section] = hubStore.hubSections;
    expect(section.subtitle).toBe('');
    expect(section.display_order).toBe(0);
    expect(section.portal_ids).toEqual([]);
    // The old shape crashed here on `portal_ids.map`.
    expect(hubStore.getSectionPortals(section)).toEqual([]);
    expect(hubStore.getUnassignedPortals()).toEqual([{ id: 1, name: 'Portal' }]);
  });

  it('cancels a pending debounced save on reset', async () => {
    vi.useFakeTimers();
    try {
      mocks.hub.get.mockResolvedValue({
        config: { sections: [] },
        portals: [],
        open_request_count: 0,
      });
      await hubStore.loadHub();
      // Clear the initial-load save guard.
      await vi.advanceTimersByTimeAsync(100);

      hubStore.saveCustomizations();
      hubStore.reset();
      await vi.advanceTimersByTimeAsync(2000);

      // A cancelled debounce must not fire after reset and overwrite the hub
      // with default state.
      expect(mocks.hub.updateConfig).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('preserves explicit section assignments', async () => {
    mocks.hub.get.mockResolvedValue({
      config: {
        sections: [
          {
            id: 'a',
            title: 'Devices',
            subtitle: 'Managed hardware',
            display_order: 2,
            portal_ids: [1],
          },
        ],
      },
      portals: [{ id: 1, name: 'Portal' }],
      open_request_count: 0,
    });

    await hubStore.loadHub();

    const section = hubStore.hubSections[0];
    expect(section.subtitle).toBe('Managed hardware');
    expect(section.display_order).toBe(2);
    expect(hubStore.getSectionPortals(section).map((portal) => portal.id)).toEqual([1]);
    expect(hubStore.getUnassignedPortals()).toEqual([]);
  });
});
