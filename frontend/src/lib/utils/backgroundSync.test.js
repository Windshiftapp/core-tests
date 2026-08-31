import { describe, expect, it, vi } from 'vitest';
import {
  canRunBackgroundSync,
  isExpectedBackgroundSyncError,
  onBackgroundSyncAvailable,
} from './backgroundSync.js';

describe('background sync availability', () => {
  it('pauses while hidden or offline', () => {
    expect(canRunBackgroundSync({ document: { hidden: true }, navigator: { onLine: true } })).toBe(
      false
    );
    expect(
      canRunBackgroundSync({ document: { hidden: false }, navigator: { onLine: false } })
    ).toBe(false);
    expect(canRunBackgroundSync({ document: { hidden: false }, navigator: { onLine: true } })).toBe(
      true
    );
  });

  it('recognizes expected connectivity and teardown failures', () => {
    expect(isExpectedBackgroundSyncError({ name: 'AbortError' })).toBe(true);
    expect(isExpectedBackgroundSyncError({ code: 'NETWORK_ERROR' })).toBe(true);
    expect(isExpectedBackgroundSyncError({ code: 'REQUEST_TIMEOUT' })).toBe(true);
    expect(isExpectedBackgroundSyncError(new Error('server bug'))).toBe(false);
  });

  it('refreshes on foreground/online transitions and removes its listeners', () => {
    const windowRef = new EventTarget();
    const documentRef = new EventTarget();
    const navigatorRef = { onLine: false };
    Object.assign(documentRef, { hidden: true, visibilityState: 'hidden' });
    const callback = vi.fn();

    const stop = onBackgroundSyncAvailable(callback, {
      document: documentRef,
      navigator: navigatorRef,
      window: windowRef,
    });

    windowRef.dispatchEvent(new Event('online'));
    expect(callback).not.toHaveBeenCalled();

    navigatorRef.onLine = true;
    Object.assign(documentRef, { hidden: false, visibilityState: 'visible' });
    documentRef.dispatchEvent(new Event('visibilitychange'));
    expect(callback).toHaveBeenCalledTimes(1);

    stop();
    windowRef.dispatchEvent(new Event('online'));
    expect(callback).toHaveBeenCalledTimes(1);
  });
});
