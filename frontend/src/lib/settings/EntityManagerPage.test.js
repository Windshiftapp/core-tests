import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, expect, test, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../composables/useConfirm.js', () => ({ confirm: vi.fn(async () => true) }));
vi.mock('../utils/keyboardShortcuts.js', () => ({
  toHotkeyString: () => 'A',
  getShortcut: () => null,
  matchesShortcut: () => false,
  getDisplayString: () => '',
}));

import { confirm } from '../composables/useConfirm.js';
import EntityManagerPageHarness from './EntityManagerPageHarness.svelte';

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

test('loads rows and creates through the modal', async () => {
  const load = vi.fn(async () => [{ id: 1, name: 'Alpha' }]);
  const create = vi.fn(async () => ({}));

  render(EntityManagerPageHarness, {
    props: { load, create, update: vi.fn(), remove: vi.fn() },
  });

  await screen.findByTestId('widget-row-1');

  await fireEvent.click(screen.getByTestId('widget-add'));
  await fireEvent.input(screen.getByTestId('widget-name'), { target: { value: 'Beta' } });
  await fireEvent.click(screen.getByTestId('entity-form-confirm'));

  await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  // Initial load plus the reload after the create.
  expect(load).toHaveBeenCalledTimes(2);
});

test('edits a row through the modal', async () => {
  const load = vi.fn(async () => [{ id: 2, name: 'Alpha' }]);
  const update = vi.fn(async () => ({}));

  render(EntityManagerPageHarness, {
    props: { load, create: vi.fn(), update, remove: vi.fn() },
  });

  await fireEvent.click(await screen.findByTestId('widget-edit-2'));
  await fireEvent.input(screen.getByTestId('widget-name'), { target: { value: 'Gamma' } });
  await fireEvent.click(screen.getByTestId('entity-form-confirm'));

  await waitFor(() => expect(update).toHaveBeenCalledWith(2));
});

test('confirms before deleting a row', async () => {
  const load = vi.fn(async () => [{ id: 5, name: 'Alpha' }]);
  const remove = vi.fn(async () => ({}));

  render(EntityManagerPageHarness, {
    props: { load, create: vi.fn(), update: vi.fn(), remove },
  });

  await fireEvent.click(await screen.findByTestId('widget-delete-5'));

  await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(1));
});

test('renders the load error when loading fails', async () => {
  const load = vi.fn(async () => {
    throw new Error('nope');
  });

  render(EntityManagerPageHarness, {
    props: { load, create: vi.fn(), update: vi.fn(), remove: vi.fn() },
  });

  // The scaffold prefers the thrown message over the generic label.
  expect(await screen.findByText('nope')).toBeInTheDocument();
});
