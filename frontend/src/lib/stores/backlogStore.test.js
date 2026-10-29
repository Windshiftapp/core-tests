import { beforeEach, describe, expect, it, vi } from 'vitest';
import { items } from '../api/items.js';


vi.mock('../api/items.js', () => ({
  items: { getBacklog: vi.fn() },
}));


const { backlogStore } = await import('./backlogStore.svelte.js');

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe('BacklogStore request lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    backlogStore.reset();
  });

  it('keeps the active workspace count when an older request resolves last', async () => {
    const first = deferred();
    const second = deferred();
    items.getBacklog.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

    const firstLoad = backlogStore.load(1);
    const secondLoad = backlogStore.load(2);

    second.resolve({ pagination: { total_items: 22 } });
    await secondLoad;
    first.resolve({ pagination: { total_items: 11 } });
    await firstLoad;

    expect(backlogStore.workspaceId).toBe(2);
    expect(backlogStore.count).toBe(22);
  });

  it('does not overwrite a component-provided count with an older request', async () => {
    const pending = deferred();
    items.getBacklog.mockReturnValueOnce(pending.promise);

    const load = backlogStore.load(1);
    backlogStore.setCount(1, 7);
    pending.resolve({ pagination: { total_items: 3 } });
    await load;

    expect(backlogStore.count).toBe(7);
  });
});
