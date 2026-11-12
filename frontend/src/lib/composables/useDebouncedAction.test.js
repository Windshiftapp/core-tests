import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { surfaceError } = vi.hoisted(() => ({ surfaceError: vi.fn() }));

vi.mock('../stores/toasts.svelte.js', () => ({ surfaceError }));

const { useDebouncedAction } = await import('./useDebouncedAction.svelte.js');

describe('useDebouncedAction', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    surfaceError.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('runs the callback after the wait', async () => {
    const callback = vi.fn().mockResolvedValue('ok');
    const run = useDebouncedAction(callback, 50);

    run();
    await vi.advanceTimersByTimeAsync(50);

    expect(callback).toHaveBeenCalledTimes(1);
    expect(surfaceError).not.toHaveBeenCalled();
  });

  it('resolves the cancel sentinel instead of rejecting with it', async () => {
    const callback = vi.fn();
    const run = useDebouncedAction(callback, 50);

    const pending = run();
    await run.cancel();

    // Without the wrapper this promise rejects with the bare string
    // "Cancelled" and the global handler turns it into a toast.
    await expect(pending).resolves.toBeUndefined();
    expect(callback).not.toHaveBeenCalled();
    expect(surfaceError).not.toHaveBeenCalled();
  });

  it('surfaces a genuine callback rejection', async () => {
    const callback = vi.fn().mockRejectedValue(new Error('save failed'));
    const run = useDebouncedAction(callback, 50);

    run();
    await vi.advanceTimersByTimeAsync(50);

    expect(surfaceError).toHaveBeenCalledWith(expect.any(Error), {
      context: 'useDebouncedAction',
    });
  });
});
