import { fireEvent, render } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('../../api/workspaces.js', () => ({
  workspaces: {
    getPage: vi.fn(),
    get: vi.fn(),
    getOrCreatePersonal: vi.fn(),
  },
}));

vi.mock('../../api/pages.js', () => ({
  pages: {
    getAll: vi.fn(() => Promise.resolve([])),
    movePage: vi.fn(),
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import { workspaces } from '../../api/workspaces.js';
import { workspacesStore } from '../../stores/workspaces.svelte.js';
import PageMoveDialog from './PageMoveDialog.svelte';

function pageDocument(rows, total = rows.length) {
  return { data: rows, pagination: { page: 1, page_size: 200, total, total_pages: 1 } };
}

beforeEach(() => {
  workspacesStore.clear();
  vi.clearAllMocks();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = '';
});

test('searches workspaces on the server so a destination beyond the cached page is reachable', async () => {
  // The directory cache only holds one bounded page; the destination here is
  // deliberately absent from it and only reachable through searchWorkspaces.
  workspaces.getPage.mockImplementation((filters) => {
    const search = filters?.search || '';
    if (!search) {
      return Promise.resolve(
        pageDocument([
          { id: 1, name: 'Alpha', is_personal: false, active: true },
          { id: 2, name: 'Beta', is_personal: false, active: true },
        ])
      );
    }
    const matches =
      search.toLowerCase() === 'zeta'
        ? [{ id: 387, name: 'Zeta destination', is_personal: false, active: true }]
        : [];
    return Promise.resolve(pageDocument(matches, matches.length));
  });

  render(PageMoveDialog, {
    props: {
      isOpen: true,
      workspaceId: 1,
      page: { id: 50, title: 'Portable handbook', parent_id: null },
    },
  });

  // Flush the cached directory load and the candidate fetch.
  await vi.advanceTimersByTimeAsync(0);
  await vi.advanceTimersByTimeAsync(0);

  const input = document.querySelector('#page-move-workspace-picker');
  await fireEvent.click(input);
  await fireEvent.input(input, { target: { value: 'zeta' } });

  // Debounce (300ms), then the server search replaces the option list.
  await vi.advanceTimersByTimeAsync(400);
  await vi.advanceTimersByTimeAsync(0);

  expect(workspaces.getPage).toHaveBeenCalledWith(expect.objectContaining({ search: 'zeta' }));
  expect(
    document.querySelector('[data-testid="page-move-workspace-option-387"]')
  ).toBeInTheDocument();
  expect(
    document.querySelector('[data-testid="page-move-workspace-option-1"]')
  ).not.toBeInTheDocument();
});
