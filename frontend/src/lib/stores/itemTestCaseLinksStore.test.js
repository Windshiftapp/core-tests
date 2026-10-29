import { beforeEach, describe, expect, it, vi } from 'vitest';
import { links } from '../api/configuration.js';


vi.mock('../api/configuration.js', () => ({
  links: { getForItems: vi.fn() },
}));


const { itemTestCaseLinksStore } = await import('./itemTestCaseLinksStore.svelte.js');

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function testCaseGroup(itemId, testCaseId, title, workspaceId) {
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
          target_workspace_id: workspaceId,
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
    links.getForItems.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

    itemTestCaseLinksStore.initialize(1);
    const firstLoad = itemTestCaseLinksStore.loadForItems([10]);

    itemTestCaseLinksStore.initialize(2);
    const secondLoad = itemTestCaseLinksStore.loadForItems([10]);

    second.resolve(testCaseGroup(10, 222, 'Workspace two case', 2));
    await secondLoad;

    first.resolve(testCaseGroup(10, 111, 'Workspace one case', 1));
    await firstLoad;

    expect(itemTestCaseLinksStore.workspaceId).toBe(2);
    expect(itemTestCaseLinksStore.get(10)).toEqual([
      { id: 222, title: 'Workspace two case', type: 'test_case', workspaceId: 2 },
    ]);
  });

  it('keeps the linked test case workspace id for the preview modal', async () => {
    links.getForItems.mockResolvedValue(testCaseGroup(10, 333, 'Case in workspace 7', 7));

    itemTestCaseLinksStore.initialize(1);
    await itemTestCaseLinksStore.loadForItems([10]);

    expect(itemTestCaseLinksStore.get(10)).toEqual([
      { id: 333, title: 'Case in workspace 7', type: 'test_case', workspaceId: 7 },
    ]);
  });

  it('keeps the workspace id when the item is the link target', async () => {
    links.getForItems.mockResolvedValue({
      10: {
        outgoing: [],
        incoming: [
          {
            link_type_id: 1,
            source_type: 'test_case',
            source_id: 444,
            source_title: 'Incoming case',
            source_workspace_id: 9,
            target_type: 'item',
            target_id: 10,
          },
        ],
      },
    });

    itemTestCaseLinksStore.initialize(1);
    await itemTestCaseLinksStore.loadForItems([10]);

    expect(itemTestCaseLinksStore.get(10)).toEqual([
      { id: 444, title: 'Incoming case', type: 'test_case', workspaceId: 9 },
    ]);
  });
});
