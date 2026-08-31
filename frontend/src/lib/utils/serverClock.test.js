import { beforeEach, describe, expect, test, vi } from 'vitest';

describe('server clock sampling', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.restoreAllMocks();
  });

  test('averages the two middle offsets for an even sample count', async () => {
    const now = vi.spyOn(Date, 'now');
    now.mockReturnValueOnce(1_000).mockReturnValueOnce(1_000);
    const clock = await import('./serverClock.js');

    clock.updateOffset(new Date(2_000).toUTCString());
    clock.updateOffset(new Date(4_000).toUTCString());

    expect(clock.getClockOffset()).toBe(2_000);
  });
});
