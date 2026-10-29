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
vi.mock('../stores/timerStore.svelte.js', () => ({ timerStore: { activeTimer: null } }));
vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../commands/buckets.js', () => ({ BUCKET_LABEL_KEYS: {} }));
vi.mock('../commands/context.js', () => ({ buildContext: vi.fn((ctx) => ctx) }));
vi.mock('../commands/buildCommands.js', () => ({
  buildCommands: vi.fn((ctx) =>
    (ctx.workItems ?? []).map((it) => ({
      id: `wi-${it.id}`,
      label: it.title,
      bucket: 'workItems',
    }))
  ),
}));
vi.mock('../commands/rank.js', () => ({ rankCommands: vi.fn((_q, cmds) => cmds) }));
vi.mock('../commands/executor.js', () => ({ executeCommand: vi.fn() }));
vi.mock('../commands/providers/mobileNavigationProvider.js', () => ({
  mobileNavigationProvider: {},
}));
vi.mock('../commands/providers/pageSearchProvider.js', () => ({ pageSearchProvider: {} }));
vi.mock('../commands/providers/searchProvider.js', () => ({ searchProvider: {} }));
vi.mock('./mobileUrls.js', () => ({ toMobileUrl: (url) => url }));
vi.mock('./mobilePagesData.js', () => ({ searchPagesAcrossWorkspaces: vi.fn() }));

import { search } from '../api/misc.js';
import { searchPagesAcrossWorkspaces } from './mobilePagesData.js';
import { mobilePalette } from './mobilePalette.svelte.js';
import MobileCommandPalette from './MobileCommandPalette.svelte';

function deferred() {
  let resolve;
  const promise = new Promise((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

async function flush() {
  for (let i = 0; i < 12; i += 1) await Promise.resolve();
  await tick();
}

async function typeQuery(text) {
  await fireEvent.input(screen.getByTestId('mobile-command-palette-input'), {
    target: { value: text },
  });
}

describe('MobileCommandPalette — search ownership', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
    search.items.mockReset();
    searchPagesAcrossWorkspaces.mockReset();
    searchPagesAcrossWorkspaces.mockResolvedValue([]);
    mobilePalette.close();
  });

  afterEach(() => {
    mobilePalette.close();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  test('cancels the queued search when the palette closes', async () => {
    mobilePalette.open();
    render(MobileCommandPalette);
    await tick();
    await typeQuery('bug');

    mobilePalette.close();
    await tick();
    await vi.advanceTimersByTimeAsync(300);

    expect(search.items).not.toHaveBeenCalled();
  });

  test('does not resurrect a stale response that resolves after reopen', async () => {
    const pending = deferred();
    search.items.mockReturnValue(pending.promise);

    mobilePalette.open();
    render(MobileCommandPalette);
    await tick();
    await typeQuery('bug');
    await vi.advanceTimersByTimeAsync(300);
    expect(search.items).toHaveBeenCalledTimes(1);

    mobilePalette.close();
    await tick();
    mobilePalette.open();
    await tick();
    await screen.findByTestId('mobile-command-palette-input');

    pending.resolve([{ id: 1, title: 'Stale bug' }]);
    await flush();

    expect(screen.queryByTestId('mobile-command-palette-option-wi-1')).not.toBeInTheDocument();
    expect(screen.getByTestId('mobile-command-palette-empty')).toBeInTheDocument();
  });

  test('shows results returned for the current query', async () => {
    search.items.mockResolvedValue([{ id: 7, title: 'Live bug' }]);

    mobilePalette.open();
    render(MobileCommandPalette);
    await tick();
    await typeQuery('bug');
    await vi.advanceTimersByTimeAsync(300);
    await flush();

    expect(screen.getByTestId('mobile-command-palette-option-wi-7')).toHaveTextContent('Live bug');
  });
});
