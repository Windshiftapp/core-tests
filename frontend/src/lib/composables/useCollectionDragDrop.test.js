import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  updateFracIndex: vi.fn(async (id) => ({ id })),
}));

vi.mock('../api/items.js', () => ({
  items: {
    updateFracIndex: mocks.updateFracIndex,
  },
}));

import { useCollectionDragDrop } from './useCollectionDragDrop.js';

const SIBLINGS = [{ id: 1 }, { id: 2 }, { id: 3 }, { id: 4 }];
const DRAGGED = { id: 2 };
const TARGET = { id: 4 };

function setup(overrides = {}) {
  const reload = vi.fn();
  const beforeReorder = vi.fn(async ({ draggedItem }) => ({ item: draggedItem }));
  const getSiblingItems = vi.fn(() => SIBLINGS);

  const drop = useCollectionDragDrop({
    reload,
    beforeReorder,
    getSiblingItems,
    ...overrides,
  });

  return { drop, reload, beforeReorder, getSiblingItems };
}

beforeEach(() => {
  vi.useFakeTimers();
  mocks.updateFracIndex.mockClear();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useCollectionDragDrop', () => {
  test('sends the neighbours for a top-edge drop', async () => {
    const { drop, reload } = setup();

    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });

    expect(mocks.updateFracIndex).toHaveBeenCalledTimes(1);
    expect(mocks.updateFracIndex).toHaveBeenCalledWith(2, {
      previous_item_id: 3,
      next_item_id: 4,
    });
    expect(reload).toHaveBeenCalledTimes(1);
  });

  test('sends the neighbours for a bottom-edge drop', async () => {
    const { drop } = setup();

    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'bottom', { sectionId: '7' });

    expect(mocks.updateFracIndex).toHaveBeenCalledWith(2, {
      previous_item_id: 4,
      next_item_id: null,
    });
  });

  test('issues no reorder when the drop would not move the item', async () => {
    const { drop, reload } = setup();

    // Item 3 already sits directly below item 2, so a bottom-edge drop on item 2
    // would not move it.
    await drop.handleEdgeBasedDrop({ id: 3 }, { id: 2 }, 'bottom', { sectionId: '7' });

    expect(mocks.updateFracIndex).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
  });

  test('reorders a cross-scope drop even when the sibling indices are adjacent', async () => {
    const { drop, beforeReorder } = setup();
    beforeReorder.mockResolvedValue({ item: { id: 2 }, checkSamePosition: false });

    await drop.handleEdgeBasedDrop(DRAGGED, { id: 3 }, 'top', { sectionId: '7' });

    expect(mocks.updateFracIndex).toHaveBeenCalledWith(2, {
      previous_item_id: 1,
      next_item_id: 3,
    });
  });

  test('aborts without reordering when beforeReorder declines the drop', async () => {
    const { drop, reload, beforeReorder } = setup();
    beforeReorder.mockResolvedValue(null);

    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });

    expect(mocks.updateFracIndex).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
  });

  test('runs one reorder for repeated events of the same drop', async () => {
    const { drop } = setup();

    const first = drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });
    const second = drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });
    await Promise.all([first, second]);

    expect(mocks.updateFracIndex).toHaveBeenCalledTimes(1);
  });

  test('allows the same drop again after the guard window elapses', async () => {
    const { drop } = setup();

    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });
    vi.advanceTimersByTime(500);
    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });

    expect(mocks.updateFracIndex).toHaveBeenCalledTimes(2);
  });

  test('reloads to recover from a rank-ordering error', async () => {
    const { drop, reload } = setup();
    mocks.updateFracIndex.mockRejectedValueOnce(Object.assign(new Error('rank'), { status: 500 }));

    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });

    expect(reload).toHaveBeenCalledTimes(1);
  });

  test('does not reload on a non-rank error', async () => {
    const { drop, reload } = setup();
    mocks.updateFracIndex.mockRejectedValueOnce(Object.assign(new Error('boom'), { status: 400 }));

    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });

    expect(reload).not.toHaveBeenCalled();
  });

  test('passes the prepared item and drop target to the sibling lookup', async () => {
    const { drop, getSiblingItems, beforeReorder } = setup();
    const prepared = { id: 2, iteration_id: 9 };
    beforeReorder.mockResolvedValue({ item: prepared });

    await drop.handleEdgeBasedDrop(DRAGGED, TARGET, 'top', { sectionId: '7' });

    expect(getSiblingItems).toHaveBeenCalledWith({
      item: prepared,
      targetItem: TARGET,
      target: { sectionId: '7' },
    });
  });
});
