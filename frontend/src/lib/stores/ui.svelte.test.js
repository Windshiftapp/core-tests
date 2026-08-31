import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const SIDEBAR_WIDTH_KEY = 'windshift-ws-sidebar-width';
const storedValues = new Map();
const localStorageMock = {
  clear: () => storedValues.clear(),
  getItem: (key) => storedValues.get(key) ?? null,
  setItem: (key, value) => storedValues.set(key, String(value)),
};

beforeEach(() => {
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: localStorageMock,
  });
});

async function loadUIStoreWithWidth(width) {
  window.localStorage.clear();
  if (width !== undefined) {
    window.localStorage.setItem(SIDEBAR_WIDTH_KEY, String(width));
  }
  vi.resetModules();
  return await import('./ui.svelte.js');
}

afterEach(() => {
  window.localStorage.clear();
  vi.resetModules();
});

describe('workspace sidebar width persistence', () => {
  it('restores widths across the full range allowed by the resizer', async () => {
    const { uiStore } = await loadUIStoreWithWidth(420);

    expect(uiStore.wsSidebarWidth).toBe(420);
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_KEY)).toBe('420');
  });

  it('falls back to the default for widths outside the resizer range', async () => {
    const { uiStore } = await loadUIStoreWithWidth(521);

    expect(uiStore.wsSidebarWidth).toBe(256);
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_KEY)).toBe('256');
  });
});
