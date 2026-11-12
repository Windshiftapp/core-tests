import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, expect, it, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

const { default: ColumnSelector } = await import('./ColumnSelector.svelte');

afterEach(cleanup);

const COLUMNS = [
  { field_identifier: 'key', field_type: 'system', display_order: 0, width: 2 },
  { field_identifier: 'title', field_type: 'system', display_order: 1, width: 2 },
  { field_identifier: 'status', field_type: 'system', display_order: 2, width: 2 },
];

it('adds and removes columns and applies the new set', async () => {
  const onchange = vi.fn();
  render(ColumnSelector, { props: { columns: COLUMNS, onchange } });

  await fireEvent.click(screen.getByTestId('column-selector-trigger'));
  await screen.findByTestId('column-selector-menu');

  await fireEvent.click(screen.getByTestId('remove-column-status'));
  await fireEvent.click(screen.getByTestId('add-column-priority'));
  await fireEvent.click(screen.getByTestId('column-selector-apply'));

  expect(onchange).toHaveBeenCalledWith({
    columns: [
      { field_identifier: 'key', field_type: 'system', display_order: 0, width: 2 },
      { field_identifier: 'title', field_type: 'system', display_order: 1, width: 2 },
      { field_identifier: 'priority', field_type: 'system', display_order: 2, width: 2 },
    ],
  });
});

it('keeps required columns and discards edits on cancel', async () => {
  const onchange = vi.fn();
  render(ColumnSelector, { props: { columns: COLUMNS, onchange } });

  await fireEvent.click(screen.getByTestId('column-selector-trigger'));
  await screen.findByTestId('column-selector-menu');

  expect(screen.queryByTestId('remove-column-key')).not.toBeInTheDocument();
  expect(screen.queryByTestId('remove-column-title')).not.toBeInTheDocument();

  await fireEvent.click(screen.getByTestId('remove-column-status'));
  // Cancel restores the original set and never calls onchange.
  const cancelButtons = screen.getAllByText('Cancel');
  await fireEvent.click(cancelButtons[cancelButtons.length - 1]);

  expect(onchange).not.toHaveBeenCalled();
});
