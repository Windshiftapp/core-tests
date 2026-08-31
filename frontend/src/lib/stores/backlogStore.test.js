import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: { items: { getBacklog: vi.fn() } },
}));

const { api } = await import('../api.js');
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
    api.items.getBacklog.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

    const firstLoad = backlogStore.load(1);
    const secondLoad = backlogStore.load(2);

    second.resolve({ pagination: { total: 22 } });
    await secondLoad;
    first.resolve({ pagination: { total: 11 } });
    await firstLoad;

    expect(backlogStore.workspaceId).toBe(2);
    expect(backlogStore.count).toBe(22);
  });

  it('does not overwrite a component-provided count with an older request', async () => {
    const pending = deferred();
    api.items.getBacklog.mockReturnValueOnce(pending.promise);

    const load = backlogStore.load(1);
    backlogStore.setCount(1, 7);
    pending.resolve({ pagination: { total: 3 } });
    await load;

    expect(backlogStore.count).toBe(7);
  });
});
