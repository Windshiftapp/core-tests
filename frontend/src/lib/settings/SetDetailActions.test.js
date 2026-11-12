import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import SetDetailActions from './SetDetailActions.svelte';

afterEach(cleanup);

describe('SetDetailActions', () => {
  test('shows the unsaved indicator and saves when there are changes', async () => {
    const onSave = vi.fn();
    render(SetDetailActions, {
      props: { hasUnsavedChanges: true, onSave, onCancel: vi.fn() },
    });

    expect(screen.getByText('settings.configSets.unsavedChanges')).toBeInTheDocument();
    const save = screen.getByRole('button', { name: 'common.save' });
    expect(save).toBeEnabled();

    await fireEvent.click(save);
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  test('disables save and hides the indicator without changes', () => {
    render(SetDetailActions, {
      props: { hasUnsavedChanges: false, onSave: vi.fn(), onCancel: vi.fn() },
    });

    expect(screen.queryByText('settings.configSets.unsavedChanges')).toBeNull();
    expect(screen.getByRole('button', { name: 'common.save' })).toBeDisabled();
  });

  test('disables save and shows the saving label while saving', () => {
    render(SetDetailActions, {
      props: { hasUnsavedChanges: true, saving: true, onSave: vi.fn(), onCancel: vi.fn() },
    });

    expect(screen.getByRole('button', { name: 'common.saving' })).toBeDisabled();
  });

  test('hides the indicator when asked and forwards the save test id', () => {
    render(SetDetailActions, {
      props: {
        hasUnsavedChanges: true,
        showUnsaved: false,
        saveTestid: 'approval-set-save',
        onSave: vi.fn(),
        onCancel: vi.fn(),
      },
    });

    expect(screen.queryByText('settings.configSets.unsavedChanges')).toBeNull();
    expect(screen.getByTestId('approval-set-save')).toBeInTheDocument();
  });

  test('cancels through the cancel button', async () => {
    const onCancel = vi.fn();
    render(SetDetailActions, {
      props: { hasUnsavedChanges: true, onSave: vi.fn(), onCancel },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'common.cancel' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
