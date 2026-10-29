import { render } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createConnectionReconcileTracker, normalizeItemEventStreamID } from './useItemEventStream.svelte.js';
import Harness from './useItemEventStreamHarness.svelte';

function installFakeEventSource() {
  const instances = [];
  class FakeEventSource {
    constructor(url) {
      this.url = url;
      this.listeners = new Map();
      this.closed = false;
      instances.push(this);
    }
    addEventListener(type, callback) {
      if (!this.listeners.has(type)) this.listeners.set(type, []);
      this.listeners.get(type).push(callback);
    }
    emit(type) {
      for (const callback of this.listeners.get(type) ?? []) callback();
    }
    close() {
      this.closed = true;
    }
  }
  globalThis.EventSource = FakeEventSource;
  return {
    instances,
    restore: () => {
      delete globalThis.EventSource;
    },
  };
}

describe('normalizeItemEventStreamID', () => {
  it('keeps route and API representations on the same stream key', () => {
    expect(normalizeItemEventStreamID('42')).toBe('42');
    expect(normalizeItemEventStreamID(42)).toBe('42');
  });

  it('rejects missing item IDs', () => {
    expect(normalizeItemEventStreamID(null)).toBeNull();
    expect(normalizeItemEventStreamID(0)).toBeNull();
  });
});

describe('createConnectionReconcileTracker', () => {
  it('does not reconcile the initial healthy connection', () => {
    const tracker = createConnectionReconcileTracker();

    expect(tracker.markConnected()).toBe(false);
  });

  it('reconciles after a connected stream disconnects', () => {
    const tracker = createConnectionReconcileTracker();
    tracker.markConnected();
    tracker.markDisconnected();

    expect(tracker.markConnected()).toBe(true);
  });

  it('reconciles when the stream errors before its first connected event', () => {
    const tracker = createConnectionReconcileTracker();
    tracker.markDisconnected();

    expect(tracker.markConnected()).toBe(true);
  });
});

describe('useItemEventStream connection handling', () => {
  let fakeEventSource;

  beforeEach(() => {
    vi.useFakeTimers();
    fakeEventSource = installFakeEventSource();
  });

  afterEach(() => {
    vi.useRealTimers();
    fakeEventSource.restore();
  });

  it('reconciles on reconnect but not on the first healthy connection', async () => {
    const onReconcile = vi.fn();
    render(Harness, { props: { itemId: 1, onReconcile } });
    expect(fakeEventSource.instances).toHaveLength(1);

    fakeEventSource.instances[0].emit('connected');
    await vi.advanceTimersByTimeAsync(250);
    expect(onReconcile).not.toHaveBeenCalled();

    fakeEventSource.instances[0].onerror?.();
    fakeEventSource.instances[0].emit('connected');
    await vi.advanceTimersByTimeAsync(250);

    expect(onReconcile).toHaveBeenCalledTimes(1);
  });

  it('coalesces rapid change frames into one reconcile', async () => {
    const onReconcile = vi.fn().mockResolvedValue(undefined);
    render(Harness, { props: { itemId: 1, onReconcile } });
    fakeEventSource.instances[0].emit('connected');
    await vi.advanceTimersByTimeAsync(250);
    onReconcile.mockClear();

    fakeEventSource.instances[0].emit('changed');
    fakeEventSource.instances[0].emit('changed');
    fakeEventSource.instances[0].emit('changed');
    await vi.advanceTimersByTimeAsync(250);

    expect(onReconcile).toHaveBeenCalledTimes(1);
  });

  it('tears down on deleted without reconciling', async () => {
    const onReconcile = vi.fn();
    const onDeleted = vi.fn();
    render(Harness, { props: { itemId: 1, onReconcile, onDeleted } });
    fakeEventSource.instances[0].emit('connected');
    await vi.advanceTimersByTimeAsync(250);
    onReconcile.mockClear();

    fakeEventSource.instances[0].emit('deleted');
    await vi.advanceTimersByTimeAsync(250);

    expect(onDeleted).toHaveBeenCalledTimes(1);
    expect(onReconcile).not.toHaveBeenCalled();
  });

  it('ignores a reconcile failure that lands after the stream identity changed', async () => {
    let rejectReconcile;
    const onReconcile = vi
      .fn()
      .mockReturnValueOnce(new Promise((_resolve, reject) => (rejectReconcile = reject)))
      .mockResolvedValue(undefined);
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { rerender } = render(Harness, {
      props: { itemId: 1, onReconcile },
    });
    fakeEventSource.instances[0].emit('connected');
    await vi.advanceTimersByTimeAsync(250);

    fakeEventSource.instances[0].emit('changed');
    await vi.advanceTimersByTimeAsync(250);
    expect(onReconcile).toHaveBeenCalledTimes(1);

    // Navigate to another item while the reconcile is still in flight.
    await rerender({ itemId: 2, onReconcile });

    rejectReconcile(new Error('offline'));
    await vi.advanceTimersByTimeAsync(0);

    // The old stream's failure must not be reported against the new item's view.
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });
});
