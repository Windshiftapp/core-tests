import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

const { default: BacklogFieldSelector } = await import('./BacklogFieldSelector.svelte');

afterEach(() => {
  cleanup();
  // The popover is portalled to document.body; wipe it so each test starts clean.
  document.body.innerHTML = '';
});

async function openMenu() {
  await fireEvent.click(screen.getByTestId('backlog-field-selector-trigger'));
  return screen.findByTestId('backlog-field-selector-menu');
}

describe('BacklogFieldSelector', () => {
  it('applies the selected fields', async () => {
    const onchange = vi.fn();
    render(BacklogFieldSelector, { props: { fields: ['parent', 'status'], onchange } });

    await openMenu();
    await fireEvent.click(screen.getByTestId('backlog-field-priority-input'));
    await fireEvent.click(screen.getByTestId('backlog-field-selector-apply'));

    expect(onchange).toHaveBeenCalledWith(['parent', 'status', 'priority']);
  });

  it('discards edits on cancel', async () => {
    const onchange = vi.fn();
    render(BacklogFieldSelector, { props: { fields: ['parent', 'status'], onchange } });

    await openMenu();
    await fireEvent.click(screen.getByTestId('backlog-field-priority-input'));
    await fireEvent.click(screen.getByText('common.cancel'));

    expect(onchange).not.toHaveBeenCalled();
  });

  it('seeds the draft from the persisted fields when reopened after a dismissal', async () => {
    const onchange = vi.fn();
    render(BacklogFieldSelector, { props: { fields: ['parent', 'status'], onchange } });

    await openMenu();
    await fireEvent.click(screen.getByTestId('backlog-field-priority-input'));
    expect(screen.getByTestId('backlog-field-priority-input')).toBeChecked();

    // Dismiss without applying or cancelling; the edit must not survive.
    await fireEvent.keyDown(screen.getByTestId('backlog-field-selector-menu'), { key: 'Escape' });
    await waitFor(() =>
      expect(screen.queryByTestId('backlog-field-selector-menu')).not.toBeInTheDocument()
    );

    await openMenu();
    expect(screen.getByTestId('backlog-field-priority-input')).not.toBeChecked();

    await fireEvent.click(screen.getByTestId('backlog-field-selector-apply'));
    expect(onchange).toHaveBeenCalledWith(['parent', 'status']);
  });
});
