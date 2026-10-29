import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { invoke, listen } = vi.hoisted(() => ({
  invoke: vi.fn(() => Promise.resolve()),
  listen: vi.fn(() => Promise.resolve(() => {})),
}));

vi.mock('@tauri-apps/api/core', () => ({ invoke }));
vi.mock('@tauri-apps/api/event', () => ({ listen }));

// The bridge is a singleton that only initializes once, so each test needs a
// fresh module instance.
async function loadBridge() {
  vi.resetModules();
  return import('./bridge.svelte.js');
}

describe('desktop bridge readiness', () => {
  beforeEach(() => {
    invoke.mockClear();
    listen.mockClear();
    window.__TAURI_INTERNALS__ = {};
  });

  afterEach(() => {
    delete window.__TAURI_INTERNALS__;
    delete window.__WINDSHIFT_CONTEXT_PATH__;
  });

  it('reports the webapp context path so auto-log targets the right prefix', async () => {
    window.__WINDSHIFT_CONTEXT_PATH__ = '/windshift';
    const { desktopBridge } = await loadBridge();

    await desktopBridge.init();

    expect(invoke).toHaveBeenCalledWith('set_webapp_ui_ready', {
      features: ['pomodoro-settings', 'about'],
      basePath: '/windshift',
    });
  });

  it('reports an empty context path when served at the origin root', async () => {
    const { desktopBridge } = await loadBridge();

    await desktopBridge.init();

    expect(invoke).toHaveBeenCalledWith('set_webapp_ui_ready', {
      features: ['pomodoro-settings', 'about'],
      basePath: '',
    });
  });
});
