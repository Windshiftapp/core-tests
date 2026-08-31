import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    timer: {
      getActive: vi.fn(),
      start: vi.fn(),
      stop: vi.fn(),
    },
  },
}));

import { api } from '../api.js';
import { timerStore } from './timerStore.svelte.js';

describe('timerStore initialization', () => {
  beforeEach(() => {
    timerStore.reset();
    vi.clearAllMocks();
  });

  it('single-flights concurrent active-timer requests', async () => {
    let resolveTimer;
    api.timer.getActive.mockReturnValue(
      new Promise((resolve) => {
        resolveTimer = resolve;
      })
    );

    const first = timerStore.initialize();
    const second = timerStore.initialize();

    expect(api.timer.getActive).toHaveBeenCalledTimes(1);
    resolveTimer(null);
    await Promise.all([first, second]);
  });
});
