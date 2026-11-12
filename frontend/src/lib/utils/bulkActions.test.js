import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  confirm: vi.fn(),
  errorToast: vi.fn(),
}));

vi.mock('../composables/useConfirm.js', () => ({ confirm: mocks.confirm }));
vi.mock('../stores/toasts.svelte.js', () => ({ surfaceError: vi.fn(), errorToast: mocks.errorToast }));
vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import { runBulkTransitionWithScreenRetry } from './bulkActions.js';

function screenRequired(blocked) {
  const error = new Error('screen_required');
  error.status = 409;
  error.code = 'screen_required';
  error.details = { items: blocked };
  return error;
}

describe('runBulkTransitionWithScreenRetry', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('returns the result without prompting when the batch succeeds', async () => {
    const transition = vi.fn().mockResolvedValue({ transitioned_count: 2 });

    const result = await runBulkTransitionWithScreenRetry(transition, [1, 2], 5);

    expect(result).toEqual({ transitioned_count: 2 });
    expect(transition).toHaveBeenCalledOnce();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it('prompts and retries without the screen-blocked items', async () => {
    mocks.confirm.mockResolvedValue(true);
    const transition = vi
      .fn()
      .mockRejectedValueOnce(screenRequired([{ item_id: 2, key: 'WI-2' }]))
      .mockResolvedValueOnce({ transitioned_count: 1 });

    const result = await runBulkTransitionWithScreenRetry(transition, [1, 2, 3], 5);

    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(transition).toHaveBeenNthCalledWith(2, [1, 3], 5);
    expect(result).toEqual({ transitioned_count: 1 });
  });

  it('returns null and does not retry when the user declines', async () => {
    mocks.confirm.mockResolvedValue(false);
    const transition = vi.fn().mockRejectedValue(screenRequired([{ item_id: 2 }]));

    const result = await runBulkTransitionWithScreenRetry(transition, [1, 2], 5);

    expect(result).toBeNull();
    expect(transition).toHaveBeenCalledOnce();
  });

  it('reports when every selected item is blocked', async () => {
    const transition = vi
      .fn()
      .mockRejectedValue(screenRequired([{ item_id: 1 }, { item_id: 2 }]));

    const result = await runBulkTransitionWithScreenRetry(transition, [1, 2], 5);

    expect(result).toBeNull();
    expect(mocks.errorToast).toHaveBeenCalledOnce();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it('rethrows an error that is not a screen refusal', async () => {
    const failure = Object.assign(new Error('boom'), { status: 500 });
    const transition = vi.fn().mockRejectedValue(failure);

    await expect(runBulkTransitionWithScreenRetry(transition, [1], 5)).rejects.toBe(failure);
    expect(mocks.confirm).not.toHaveBeenCalled();
  });
});
