import { render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getComments: vi.fn(),
}));

vi.mock('../../api.js', () => ({
  api: {
    getComments: mocks.getComments,
  },
}));

vi.mock('../../stores', () => ({
  authStore: {
    currentUser: { id: 7 },
  },
}));

vi.mock('../../stores/notifications.js', () => ({
  subscribeToNewNotifications: () => () => {},
}));

vi.mock('../../composables/usePoller.svelte.js', () => ({
  usePoller: () => ({ poll: vi.fn() }),
}));

vi.mock('../../stores/itemLiveUpdates.svelte.js', () => ({
  itemLiveUpdates: {
    isLive: () => true,
  },
}));

vi.mock('../../editors/LazyMilkdownEditor.svelte', () => ({
  default: function MilkdownEditor() {},
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import Comments from './Comments.svelte';

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe('Comments request ordering', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('does not let the initial request overwrite a newer SSE reconciliation', async () => {
    const initial = deferred();
    const comment = {
      id: 11,
      author_id: 8,
      author_name: 'Editor',
      content: 'Arrived while the item opened',
      created_at: '2026-08-17T08:00:00Z',
      updated_at: '2026-08-17T08:00:00Z',
      source: 'user',
      is_agent: false,
      is_private: false,
    };

    mocks.getComments
      .mockImplementationOnce(() => initial.promise)
      .mockResolvedValueOnce({
        comments: [comment],
        total: 1,
        has_more: false,
      });

    render(Comments, { props: { itemId: 42 } });
    await waitFor(() => expect(mocks.getComments).toHaveBeenCalledTimes(1));

    window.dispatchEvent(new CustomEvent('item-comments-changed', { detail: { itemId: 42 } }));

    expect(await screen.findByTestId('comment-item')).toHaveTextContent('Editor');
    initial.resolve({ comments: [], total: 0, has_more: false });
    await initial.promise;
    await tick();

    expect(screen.getByTestId('comment-item')).toHaveAttribute('data-comment-id', '11');
  });
});
