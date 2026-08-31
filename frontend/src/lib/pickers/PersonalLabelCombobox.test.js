import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    personalLabels: {
      getAll: vi.fn().mockResolvedValue([]),
      create: vi.fn(),
    },
  },
}));

vi.mock('../stores', () => ({
  authStore: { currentUser: { id: 17 } },
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
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      play: () => {},
      pause: () => {},
    });
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import { api } from '../api.js';
import PersonalLabelCombobox from './PersonalLabelCombobox.svelte';

beforeEach(() => {
  api.personalLabels.create.mockReset();
});

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

describe('PersonalLabelCombobox inline creation', () => {
  test('shows and executes the create action when no label matches', async () => {
    const createdLabel = { id: 41, name: 'scm', color: '#3b82f6' };
    const onSelect = vi.fn();
    api.personalLabels.create.mockResolvedValue(createdLabel);

    render(PersonalLabelCombobox, {
      props: { labels: [], value: [], onSelect },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'scm' } });

    expect(await screen.findByText('No labels found for "scm"')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Create "scm"' }));

    await waitFor(() => {
      expect(api.personalLabels.create).toHaveBeenCalledWith({ name: 'scm', user_id: 17 });
    });
    expect(onSelect).toHaveBeenCalledWith({ value: ['scm'], labels: [createdLabel] });
  });
});
