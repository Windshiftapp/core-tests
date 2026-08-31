import { describe, expect, it } from 'vitest';
import { createConnectionReconcileTracker } from './useItemEventStream.svelte.js';

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
