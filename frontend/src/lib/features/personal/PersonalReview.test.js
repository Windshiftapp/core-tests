import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterAll, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/misc.js', () => ({
  reviews: {
        getCompletedItems: vi.fn(),
        getAll: vi.fn(),
        create: vi.fn(),
        update: vi.fn(),
      },
}))

vi.mock('../../api/configuration.js', () => ({
  itemTypes: {
        getAll: vi.fn(),
      },
}));

vi.mock('../../stores/auth.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  // The real uiStore is a subscribable store with methods attached; the
  // component both auto-subscribes ($uiStore.reviewFullscreen) and calls
  // uiStore.toggleReviewFullscreen().
  const uiStore = writable({ reviewFullscreen: false });
  uiStore.toggleReviewFullscreen = () =>
    uiStore.update((state) => ({ ...state, reviewFullscreen: !state.reviewFullscreen }));
  return {
    authStore: {
          subscribe: vi.fn((fn) => {
            fn({ user: { id: 7 } });
            return () => {};
          }),
        },
  };
})

vi.mock('../../stores/ui.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  // The real uiStore is a subscribable store with methods attached; the
  // component both auto-subscribes ($uiStore.reviewFullscreen) and calls
  // uiStore.toggleReviewFullscreen().
  const uiStore = writable({ reviewFullscreen: false });
  uiStore.toggleReviewFullscreen = () =>
    uiStore.update((state) => ({ ...state, reviewFullscreen: !state.reviewFullscreen }));
  return {
    uiStore,
  };
});

vi.mock('@lucide/svelte', () => ({
  ChevronLeft: vi.fn(() => null),
  ChevronRight: vi.fn(() => null),
  Calendar: vi.fn(() => null),
  Clock: vi.fn(() => null),
  Lightbulb: vi.fn(() => null),
  Maximize: vi.fn(() => null),
  Minimize: vi.fn(() => null),
  BookOpenCheck: vi.fn(() => null),
  FileEdit: vi.fn(() => null),
}));

vi.mock('../items/WorkItemRow.svelte', async () => ({
  default: (await import('./WorkItemRowStub.svelte')).default,
}));

vi.mock('../../editors/LazyMilkdownEditor.svelte', async () => ({
  default: (await import('./PersonalReviewEditorStub.svelte')).default,
}));


const { formatDate } = await import('../../utils/dateFormatter.js');

import PersonalReview from './PersonalReview.svelte';
import { reviews } from '../../api/misc.js';
import { itemTypes } from '../../api/configuration.js';



// WI-1419: a superseded load of completed items must not overwrite the list
// when the user has already navigated to another date. The review body is
// guarded by request generation; the completed-items section must be too.
describe('PersonalReview completed items across navigation (WI-1419)', () => {
  afterAll(() => cleanup());

  beforeEach(() => {
    localStorage.clear();
    itemTypes.getAll.mockResolvedValue([]);
    reviews.getAll.mockResolvedValue([]);
  });

  it("keeps the displayed date's completed items when an older load resolves late", async () => {
    // Deferred per date so the test controls resolution order.
    const pending = new Map();
    reviews.getCompletedItems.mockImplementation((startDate) => {
      return new Promise((resolve) => {
        pending.set(startDate, resolve);
      });
    });

    const today = formatDate(new Date());
    const yesterday = formatDate(new Date(Date.now() - 86400000));

    render(PersonalReview, { props: {} });

    // The initial (today) load is issued and stays in flight.
    await waitFor(() => expect(pending.has(today)).toBe(true));

    // Navigate to yesterday before today's load resolves: the component
    // starts a second, now-current load.
    await act(async () => {
      await fireEvent.click(screen.getByTestId('review-nav-prev'));
    });
    await waitFor(() => expect(pending.has(yesterday)).toBe(true));

    // Yesterday's results land first and are applied.
    await act(async () => {
      pending.get(yesterday)([{ id: 2, title: 'yesterday item' }]);
    });
    expect(await screen.findByTestId('stub-completed-item')).toHaveTextContent('yesterday item');

    // Today's superseded response arrives last and must be discarded.
    await act(async () => {
      pending.get(today)([{ id: 1, title: 'today item' }]);
    });

    const visible = screen.getAllByTestId('stub-completed-item');
    expect(visible).toHaveLength(1);
    expect(visible[0]).toHaveTextContent('yesterday item');
    expect(screen.queryByText('today item')).not.toBeInTheDocument();
  });
});
