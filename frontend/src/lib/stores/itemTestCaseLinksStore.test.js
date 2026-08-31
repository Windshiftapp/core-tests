import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: { links: { getForItems: vi.fn() } },
}));

const { api } = await import('../api.js');
const { itemTestCaseLinksStore } = await import('./itemTestCaseLinksStore.svelte.js');

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function testCaseGroup(itemId, testCaseId, title) {
  return {
    [itemId]: {
      incoming: [],
      outgoing: [
        {
          link_type_id: 1,
          source_type: 'item',
          source_id: itemId,
          target_type: 'test_case',
          target_id: testCaseId,
          target_title: title,
        },
      ],
    },
  };
}

describe('ItemTestCaseLinksStore workspace lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    itemTestCaseLinksStore.reset();
  });

  it('does not restore test links from a previous workspace after switching', async () => {
    const first = deferred();
    const second = deferred();
    api.links.getForItems.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

    itemTestCaseLinksStore.initialize(1);
    const firstLoad = itemTestCaseLinksStore.loadForItems([10]);

    itemTestCaseLinksStore.initialize(2);
    const secondLoad = itemTestCaseLinksStore.loadForItems([10]);

    second.resolve(testCaseGroup(10, 222, 'Workspace two case'));
    await secondLoad;

    first.resolve(testCaseGroup(10, 111, 'Workspace one case'));
    await firstLoad;

    expect(itemTestCaseLinksStore.workspaceId).toBe(2);
    expect(itemTestCaseLinksStore.get(10)).toEqual([
      { id: 222, title: 'Workspace two case', type: 'test_case' },
    ]);
  });
});
