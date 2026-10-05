import { describe, expect, it } from 'vitest';
import {
  createConnectionReconcileTracker,
  normalizeItemEventStreamID,
} from './useItemEventStream.svelte.js';

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
  it('reconciles the initial connection to cover changes before subscription', () => {
    const tracker = createConnectionReconcileTracker();

    expect(tracker.markConnected()).toBe(true);
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
