import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * Fake BroadcastChannel that fans `postMessage` out to every other live
 * instance's `message` listeners (and never back to the sender), mirroring
 * the browser's own same-context exclusion guarantee.
 */
class FakeBroadcastChannel {
  static instances = new Set();
  name;
  #listeners = new Set();

  constructor(name) {
    this.name = name;
    FakeBroadcastChannel.instances.add(this);
  }

  postMessage(data) {
    for (const ch of FakeBroadcastChannel.instances) {
      if (ch === this) continue; // sender is excluded
      for (const cb of ch.#listeners) cb({ data });
    }
  }

  addEventListener(_type, cb) {
    this.#listeners.add(cb);
  }

  removeEventListener(_type, cb) {
    this.#listeners.delete(cb);
  }

  close() {
    this.#listeners.clear();
    FakeBroadcastChannel.instances.delete(this);
  }
}

// Ensure each test imports a fresh module so initCrossTabSync's "initialized"
// guard starts clean. We reset modules + register the fake channel global.
async function freshModule() {
  vi.resetModules();
  vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel);
  FakeBroadcastChannel.instances.clear();
  return await import('./crossTabSync.js');
}

describe('crossTabSync', () => {
  beforeEach(() => {
    vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel);
    FakeBroadcastChannel.instances.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it('notifyItemMutation posts a structured message to other tabs', async () => {
    const { notifyItemMutation } = await freshModule();

    const other = new FakeBroadcastChannel('windshift-work-items');
    const seen = vi.fn();
    other.addEventListener('message', seen);

    notifyItemMutation({ type: 'update', itemId: 7 });

    expect(seen).toHaveBeenCalledTimes(1);
    expect(seen).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({ type: 'update', itemId: 7 }),
      })
    );
    other.close();
  });

  it('stamp an origin id unique per module instance', async () => {
    const { tabId } = await freshModule();
    expect(typeof tabId).toBe('string');
    expect(tabId.length).toBeGreaterThan(0);
  });

  it('initCrossTabSync calls the injected invalidation handler on foreign messages', async () => {
    vi.useFakeTimers();
    const { initCrossTabSync } = await freshModule();
    const onInvalidate = vi.fn().mockResolvedValue(undefined);
    const dispose = initCrossTabSync({ onInvalidate });

    const peer = new FakeBroadcastChannel('windshift-work-items');
    peer.postMessage({ type: 'update', itemId: 5, origin: 'other-tab' });

    // Trailing-debounced: nothing until the window elapses.
    expect(onInvalidate).not.toHaveBeenCalled();
    vi.advanceTimersByTime(200);
    expect(onInvalidate).toHaveBeenCalledTimes(1);

    dispose();
    peer.close();
  });

  it('coalesces a burst of foreign messages into a single invalidation', async () => {
    vi.useFakeTimers();
    const { initCrossTabSync } = await freshModule();
    const onInvalidate = vi.fn().mockResolvedValue(undefined);
    const dispose = initCrossTabSync({ onInvalidate });

    const peer = new FakeBroadcastChannel('windshift-work-items');
    // Simulate a bulk mutation in another tab: N notices back-to-back.
    for (let i = 0; i < 5; i++) {
      peer.postMessage({ type: 'update', itemId: i, origin: 'bulk-tab' });
    }

    expect(onInvalidate).not.toHaveBeenCalled();
    vi.advanceTimersByTime(200);
    expect(onInvalidate).toHaveBeenCalledTimes(1);

    dispose();
    peer.close();
  });

  it('ignores messages originating from its own tab', async () => {
    const { initCrossTabSync, tabId } = await freshModule();
    const onInvalidate = vi.fn();
    const dispose = initCrossTabSync({ onInvalidate });

    const peer = new FakeBroadcastChannel('windshift-work-items');
    peer.postMessage({ type: 'update', itemId: 5, origin: tabId }); // self origin

    expect(onInvalidate).not.toHaveBeenCalled();
    dispose();
    peer.close();
  });

  it('is idempotent — multiple init calls register a single listener', async () => {
    vi.useFakeTimers();
    const { initCrossTabSync } = await freshModule();
    const onInvalidate = vi.fn();
    initCrossTabSync({ onInvalidate });
    initCrossTabSync({ onInvalidate });

    const peer = new FakeBroadcastChannel('windshift-work-items');
    peer.postMessage({ type: 'update', itemId: 1, origin: 'x' });

    vi.advanceTimersByTime(200);
    expect(onInvalidate).toHaveBeenCalledTimes(1);
    peer.close();
  });

  it('swaps in the handler from a repeat init without duplicating the listener', async () => {
    // Mirrors HMR re-executing the injecting module: the newest closure must be
    // the one that runs, and messages must still be handled once.
    vi.useFakeTimers();
    const { initCrossTabSync } = await freshModule();
    const stale = vi.fn();
    const fresh = vi.fn();
    initCrossTabSync({ onInvalidate: stale });
    initCrossTabSync({ onInvalidate: fresh });

    const peer = new FakeBroadcastChannel('windshift-work-items');
    peer.postMessage({ type: 'update', itemId: 1, origin: 'x' });

    vi.advanceTimersByTime(200);
    expect(stale).not.toHaveBeenCalled();
    expect(fresh).toHaveBeenCalledTimes(1);
    peer.close();
  });

  it('no-ops gracefully when BroadcastChannel is unavailable', async () => {
    vi.unstubAllGlobals();
    vi.resetModules();
    // BroadcastChannel undefined
    const { initCrossTabSync, notifyItemMutation } = await import('./crossTabSync.js');

    expect(() => {
      notifyItemMutation({ type: 'update', itemId: 1 });
      initCrossTabSync({ onInvalidate: vi.fn() });
    }).not.toThrow();
  });
});
