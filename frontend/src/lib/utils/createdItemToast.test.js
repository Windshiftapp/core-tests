import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const toasts = [];
  return {
    toasts,
    addToast: vi.fn((options) => {
      const id = toasts.length + 1;
      toasts.push({ id, options });
      return id;
    }),
    removeToast: vi.fn((id) => {
      const index = toasts.findIndex((toast) => toast.id === id);
      if (index === -1) return;
      const [removed] = toasts.splice(index, 1);
      removed.options.onDismiss?.();
    }),
    navigate: vi.fn(),
    openItemModal: vi.fn(),
    isTypingInField: vi.fn(() => false),
  };
});

vi.mock('../router.js', () => ({ navigate: mocks.navigate }));
vi.mock('../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  addToast: mocks.addToast,
  removeToast: mocks.removeToast,
}));
vi.mock('../stores/itemModalStore.svelte.js', () => ({
  itemModalStore: { open: mocks.openItemModal },
}));
vi.mock('./keyboardShortcuts.js', () => ({
  isTypingInField: mocks.isTypingInField,
}));

import { showCreatedItemOutsideViewToast, showCreatedItemToast } from './createdItemToast.js';

const item = {
  id: 42,
  workspace_id: 7,
  workspace_key: 'WI',
  workspace_item_number: 42,
  title: 'Fix login',
};

describe('createdItemToast', () => {
  beforeEach(() => {
    mocks.toasts.length = 0;
    mocks.addToast.mockClear();
    mocks.removeToast.mockClear();
    mocks.navigate.mockClear();
    mocks.openItemModal.mockClear();
    mocks.isTypingInField.mockReset();
    mocks.isTypingInField.mockReturnValue(false);
  });

  afterEach(() => {
    // Dismiss leftovers so their document keydown listeners never leak into
    // the next test.
    for (const toast of mocks.toasts) toast.options.onDismiss?.();
    mocks.toasts.length = 0;
    vi.useRealTimers();
  });

  it('shows the item key and opens the item modal when the created card is hidden by filters', () => {
    const toastId = showCreatedItemOutsideViewToast(item, {
      message: 'The card was created in Other, but is hidden by the collection filters.',
    });

    expect(mocks.addToast).toHaveBeenCalledTimes(1);
    const options = mocks.addToast.mock.calls[0][0];
    expect(options).toMatchObject({
      title: 'WI-42 created',
      message: 'The card was created in Other, but is hidden by the collection filters.',
      variant: 'info',
      clickable: true,
      actionLabel: 'Open item',
      keyboardHint: 'O',
    });

    options.onClick();

    expect(mocks.openItemModal).toHaveBeenCalledWith(42, 7);
    expect(mocks.removeToast).toHaveBeenCalledWith(toastId);
  });

  it('uses a caller-provided onOpen instead of the default item modal', () => {
    const onOpen = vi.fn();
    showCreatedItemOutsideViewToast(item, { message: 'hidden', onOpen });

    mocks.addToast.mock.calls[0][0].onClick();

    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(mocks.openItemModal).not.toHaveBeenCalled();
  });

  it('opens the hidden item with the "O" shortcut', () => {
    showCreatedItemOutsideViewToast(item, { message: 'hidden' });

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'o' }));

    expect(mocks.openItemModal).toHaveBeenCalledWith(42, 7);
  });

  it('ignores the "O" shortcut while typing in a field', () => {
    mocks.isTypingInField.mockReturnValue(true);
    showCreatedItemOutsideViewToast(item, { message: 'hidden' });

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'o' }));

    expect(mocks.openItemModal).not.toHaveBeenCalled();
  });

  it('falls back to a WORK-<id> key when the workspace key is unavailable', () => {
    showCreatedItemOutsideViewToast(
      { id: 9, workspace_id: 1, title: 'Untyped' },
      { message: 'hidden' }
    );

    expect(mocks.addToast.mock.calls[0][0].title).toBe('WORK-9 created');
  });

  it('does not show a toast for an item without an id', () => {
    expect(showCreatedItemOutsideViewToast(null, { message: 'hidden' })).toBeNull();
    expect(showCreatedItemOutsideViewToast({}, { message: 'hidden' })).toBeNull();

    expect(mocks.addToast).not.toHaveBeenCalled();
  });

  it('keeps navigating to the item page for the visible-item success toast', () => {
    showCreatedItemToast(item);

    const options = mocks.addToast.mock.calls[0][0];
    expect(options).toMatchObject({
      title: 'WI-42 created',
      variant: 'success',
      clickable: true,
      actionLabel: 'Open item',
    });

    options.onClick();

    expect(mocks.navigate).toHaveBeenCalledWith('/workspaces/7/items/42');
  });
});
