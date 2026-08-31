import { describe, expect, it, vi } from 'vitest';
import { createAsyncLoader } from './createAsyncLoader.svelte.js';

describe('createAsyncLoader', () => {
  it('silently discards an aborted load during navigation', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const loader = createAsyncLoader(() =>
      Promise.reject(new DOMException('The document was unloaded', 'AbortError'))
    );

    await loader.load();

    expect(loader.loading).toBe(false);
    expect(loader.error).toBeNull();
    expect(loader.data).toEqual([]);
    expect(consoleError).not.toHaveBeenCalled();
    consoleError.mockRestore();
  });

  it('discards a failed load after its owner is disposed', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    let rejectLoad;
    const loader = createAsyncLoader(
      () =>
        new Promise((_, reject) => {
          rejectLoad = reject;
        })
    );

    const load = loader.load();
    loader.dispose();
    rejectLoad(new TypeError('Failed to fetch'));
    await load;

    expect(loader.loading).toBe(false);
    expect(loader.error).toBeNull();
    expect(loader.data).toEqual([]);
    expect(consoleError).not.toHaveBeenCalled();
    consoleError.mockRestore();
  });
});
