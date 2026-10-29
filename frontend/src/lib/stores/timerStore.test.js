import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/time.js', () => ({
  timer: {
        getActive: vi.fn(),
        start: vi.fn(),
        stop: vi.fn(),
      },
}));

import { timer } from '../api/time.js';
import { timerStore } from './timerStore.svelte.js';

describe('timerStore initialization', () => {
  beforeEach(() => {
    timerStore.reset();
    vi.clearAllMocks();
  });

  it('single-flights concurrent active-timer requests', async () => {
    let resolveTimer;
    timer.getActive.mockReturnValue(
      new Promise((resolve) => {
        resolveTimer = resolve;
      })
    );

    const first = timerStore.initialize();
    const second = timerStore.initialize();

    expect(timer.getActive).toHaveBeenCalledTimes(1);
    resolveTimer(null);
    await Promise.all([first, second]);
  });
});
