import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        getPage: vi.fn(),
        get: vi.fn(),
        getOrCreatePersonal: vi.fn(),
      },
}));

import { workspaces } from '../api/workspaces.js';
import { workspacesStore } from '../stores/workspaces.svelte.js';
import WorkspacePicker from './WorkspacePicker.svelte';

beforeEach(() => {
  workspacesStore.clear();
  vi.clearAllMocks();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = '';
});

function pageDocument(rows, total = rows.length) {
  return { data: rows, pagination: { page: 1, page_size: 200, total, total_pages: 1 } };
}

describe('WorkspacePicker — server-backed search (WI-1445)', () => {
  test('typing beyond the cached page pulls matches from the server', async () => {
    workspaces.getPage.mockImplementation((_filters) => {
      const search = _filters?.search || '';
      if (!search) {
        return Promise.resolve(
          pageDocument([
            { id: 1, name: 'Alpha', is_personal: false },
            { id: 2, name: 'Beta', is_personal: false },
          ])
        );
      }
      const matches = search.toLowerCase() === 'zeta'
        ? [{ id: 9, name: 'Zeta Workspace', is_personal: false }]
        : [];
      return Promise.resolve(pageDocument(matches, matches.length));
    });

    render(WorkspacePicker, { props: { value: [] } });
    // onMount loads the cached directory page.
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'zeta' } });

    // Debounce (300ms) then server search replace the option list.
    await vi.advanceTimersByTimeAsync(400);
    await vi.advanceTimersByTimeAsync(0);

    expect(workspaces.getPage).toHaveBeenCalledWith(
      expect.objectContaining({ search: 'zeta' })
    );
    expect(document.querySelector('[data-option-value="9"]')).toBeInTheDocument();
    expect(document.querySelector('[data-option-value="1"]')).not.toBeInTheDocument();

    // Clearing the query restores the cached directory page.
    await fireEvent.input(input, { target: { value: '' } });
    await vi.advanceTimersByTimeAsync(400);
    await vi.advanceTimersByTimeAsync(0);

    expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    expect(document.querySelector('[data-option-value="9"]')).not.toBeInTheDocument();
  });

  test('keeps a selected chip labeled after server search replaces the options (WI-1586)', async () => {
    workspaces.getPage.mockImplementation((_filters) => {
      const search = _filters?.search || '';
      if (!search) {
        return Promise.resolve(
          pageDocument([
            { id: 1, name: 'Alpha', is_personal: false },
            { id: 2, name: 'Beta', is_personal: false },
          ])
        );
      }
      const matches =
        search.toLowerCase() === 'zeta'
          ? [{ id: 9, name: 'Zeta Workspace', is_personal: false }]
          : [];
      return Promise.resolve(pageDocument(matches, matches.length));
    });

    render(WorkspacePicker, { props: { value: [1] } });
    await vi.waitFor(() => expect(screen.getByText('Alpha')).toBeInTheDocument());

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'zeta' } });
    await vi.advanceTimersByTimeAsync(400);
    await vi.advanceTimersByTimeAsync(0);

    // Alpha is no longer an option, but the selected chip still resolves its
    // label from the remembered directory instead of vanishing.
    expect(document.querySelector('[data-option-value="1"]')).not.toBeInTheDocument();
    expect(screen.getByText('Alpha')).toBeInTheDocument();
  });
});
