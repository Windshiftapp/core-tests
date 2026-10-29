import { fireEvent, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api/misc.js', () => ({
  search: { items: vi.fn() },
}));
vi.mock('../router.js', () => ({ navigate: vi.fn() }));
vi.mock('../stores/workspaces.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  return {
    workspacesStore: writable({
          personalWorkspace: null,
          regularWorkspaces: [{ id: 2, name: 'Acme' }],
        }),
  };
});
vi.mock('./mobilePagesData.js', () => ({ searchPagesAcrossWorkspaces: vi.fn() }));
vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import { search } from '../api/misc.js';
import { searchPagesAcrossWorkspaces } from './mobilePagesData.js';
import SearchView from './SearchView.svelte';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function item(id, title) {
  return { id, title, workspace_key: 'ACME', workspace_item_number: id };
}

async function flush() {
  for (let i = 0; i < 12; i += 1) await Promise.resolve();
  await tick();
}

async function typeQuery(text) {
  await fireEvent.input(screen.getByTestId('mobile-search-input'), { target: { value: text } });
}

describe('SearchView — mobile search ownership', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
    search.items.mockReset();
    searchPagesAcrossWorkspaces.mockReset();
    searchPagesAcrossWorkspaces.mockResolvedValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  test('renders results for the current query', async () => {
    search.items.mockResolvedValue([item(10, 'Current bug')]);

    render(SearchView);
    await typeQuery('bug');
    await vi.advanceTimersByTimeAsync(250);
    await flush();

    expect(screen.getAllByTestId('mobile-item-row')).toHaveLength(1);
    expect(screen.getByText('Current bug')).toBeInTheDocument();
  });

  test('debounces rapid input into a single request for the final query', async () => {
    search.items.mockResolvedValue([]);

    render(SearchView);
    await typeQuery('b');
    await typeQuery('bu');
    await typeQuery('bug');
    await vi.advanceTimersByTimeAsync(250);
    await flush();

    expect(search.items).toHaveBeenCalledTimes(1);
    expect(search.items.mock.calls[0][0]).toMatchObject({ query: 'bug', limit: 30 });
  });

  test('discards a late response that resolves after the query is cleared', async () => {
    const pending = deferred();
    search.items.mockReturnValue(pending.promise);

    render(SearchView);
    await typeQuery('bug');
    await vi.advanceTimersByTimeAsync(250);
    expect(search.items).toHaveBeenCalledTimes(1);

    await fireEvent.click(screen.getByTestId('mobile-search-clear'));
    expect(screen.queryByTestId('mobile-item-row')).not.toBeInTheDocument();

    pending.resolve([item(10, 'Stale bug')]);
    await flush();

    expect(screen.queryByTestId('mobile-item-row')).not.toBeInTheDocument();
    expect(screen.getByTestId('search-prompt')).toBeInTheDocument();
  });

  test('aborts and discards the in-flight request when a newer query supersedes it', async () => {
    const first = deferred();
    search.items.mockReturnValueOnce(first.promise).mockResolvedValue([]);

    render(SearchView);
    await typeQuery('bug');
    await vi.advanceTimersByTimeAsync(250);
    const firstSignal = search.items.mock.calls[0][0].signal;
    expect(firstSignal.aborted).toBe(false);

    await typeQuery('buggy');
    expect(firstSignal.aborted).toBe(true);

    first.resolve([item(10, 'Stale bug')]);
    await flush();

    expect(screen.queryByTestId('mobile-item-row')).not.toBeInTheDocument();
  });

  test('aborts the pending request when the view unmounts', async () => {
    const pending = deferred();
    search.items.mockReturnValue(pending.promise);

    const { unmount } = render(SearchView);
    await typeQuery('bug');
    await vi.advanceTimersByTimeAsync(250);
    const signal = search.items.mock.calls[0][0].signal;
    expect(signal.aborted).toBe(false);

    unmount();

    expect(signal.aborted).toBe(true);
  });
});
