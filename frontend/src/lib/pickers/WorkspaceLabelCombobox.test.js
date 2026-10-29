import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api/misc.js', () => ({
  labels: {
        getAll: vi.fn(),
        create: vi.fn(),
      },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) => {
    if (key === 'pickers.noLabelsFoundFor') return `No labels found for "${params.query}"`;
    if (key === 'pickers.createItem') return `Create "${params.value}"`;
    return key;
  },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import { labels } from '../api/misc.js';
import WorkspaceLabelCombobox from './WorkspaceLabelCombobox.svelte';

beforeEach(() => {
  labels.getAll.mockReset().mockResolvedValue([]);
  labels.create.mockReset();
});

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

describe('WorkspaceLabelCombobox', () => {
  test('loads the workspace label catalog', async () => {
    labels.getAll.mockResolvedValue([{ id: 7, name: 'github' }]);

    render(WorkspaceLabelCombobox, {
      props: { workspaceId: 23, value: [] },
    });

    await waitFor(() => expect(labels.getAll).toHaveBeenCalledWith(23));
  });

  test('creates a global label using the selected workspace authorization context', async () => {
    const createdLabel = { id: 41, name: 'scm', color: '#3b82f6' };
    const onSelect = vi.fn();
    labels.create.mockResolvedValue(createdLabel);

    render(WorkspaceLabelCombobox, {
      props: { workspaceId: 23, labels: [], value: [], onSelect },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'scm' } });
    await fireEvent.click(await screen.findByRole('button', { name: 'Create "scm"' }));

    await waitFor(() => {
      expect(labels.create).toHaveBeenCalledWith({ name: 'scm', workspace_id: 23 });
    });
    expect(onSelect).toHaveBeenCalledWith({ value: ['scm'], labels: [createdLabel] });
  });
});
