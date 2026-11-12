import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { getFields, getAllCustomFields } = vi.hoisted(() => ({
  getFields: vi.fn(),
  getAllCustomFields: vi.fn(),
}));

vi.mock('../api/configuration.js', () => ({
  screens: { getFields },
  customFields: { getAll: getAllCustomFields },
}));

vi.mock('../editors/LazyMilkdownEditor.svelte', async () => ({
  default: (await import('./TransitionDescriptionStub.svelte')).default,
}));

// The real store loads translations asynchronously; a synchronous map keeps
// the label assertions deterministic and makes the `items.title` regression
// observable (it resolves to "Items" rather than "Title").
vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params) => {
    const messages = {
      'common.title': 'Title',
      'common.description': 'Description',
      'items.title': 'Items',
      'common.loading': 'Loading',
      'transitionScreen.title': 'Transition',
      'transitionScreen.moveTo': 'Move to {status}',
      'transitionScreen.comment': 'Comment',
      'transitionScreen.commentPlaceholder': 'Add a comment (optional)',
      'transitionScreen.confirm': 'Confirm',
      'transitionScreen.failed': 'Transition failed',
      'transitionScreen.missingFields': 'Missing required fields',
      'dialogs.cancel': 'Cancel',
      'dialogs.confirm': 'Confirm',
    };
    let value = messages[key] ?? key;
    if (params) {
      for (const [name, replacement] of Object.entries(params)) {
        value = value.replace(`{${name}}`, String(replacement));
      }
    }
    return value;
  },
}));

import TransitionScreenModal from './TransitionScreenModal.svelte';

const screenSummary = { id: 7, name: 'Close Screen', field_count: 2, required_field_count: 1 };

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
  getFields.mockReset();
  getAllCustomFields.mockReset();
});

describe('TransitionScreenModal', () => {
  it('labels the title field and pre-fills displayed fields from the item', async () => {
    getFields.mockResolvedValue([
      { field_type: 'default', field_identifier: 'title', is_required: true },
      { field_type: 'default', field_identifier: 'description', is_required: false },
    ]);
    getAllCustomFields.mockResolvedValue([]);

    render(TransitionScreenModal, {
      props: {
        show: true,
        screen: screenSummary,
        item: { id: 1, title: 'Existing title', description: 'Existing description' },
      },
    });

    const titleInput = await screen.findByTestId('transition-field-title');
    expect(titleInput).toHaveValue('Existing title');
    expect(screen.getByTestId('transition-field-description')).toHaveAttribute(
      'data-content',
      'Existing description'
    );
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.queryByText('Items')).not.toBeInTheDocument();
  });

  it('submits the seeded values with the transition', async () => {
    getFields.mockResolvedValue([
      { field_type: 'default', field_identifier: 'title', is_required: true },
      { field_type: 'default', field_identifier: 'description', is_required: false },
    ]);
    getAllCustomFields.mockResolvedValue([]);

    const onconfirm = vi.fn().mockResolvedValue(undefined);
    render(TransitionScreenModal, {
      props: {
        show: true,
        screen: screenSummary,
        item: { id: 1, title: 'Existing title', description: 'Existing description' },
        onconfirm,
      },
    });

    await screen.findByTestId('transition-field-title');
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() =>
      expect(onconfirm).toHaveBeenCalledWith({
        title: 'Existing title',
        description: 'Existing description',
      })
    );
  });

  it('submits with the modal submit shortcut', async () => {
    getFields.mockResolvedValue([
      { field_type: 'default', field_identifier: 'title', is_required: true },
    ]);
    getAllCustomFields.mockResolvedValue([]);

    const onconfirm = vi.fn().mockResolvedValue(undefined);
    render(TransitionScreenModal, {
      props: {
        show: true,
        screen: screenSummary,
        item: { id: 1, title: 'Existing title' },
        onconfirm,
      },
    });

    const titleInput = await screen.findByTestId('transition-field-title');
    await fireEvent.keyDown(titleInput, { key: 'Enter', ctrlKey: true });

    await waitFor(() =>
      expect(onconfirm).toHaveBeenCalledWith({ title: 'Existing title' })
    );
  });

  it('submits with Enter from a single-line field', async () => {
    getFields.mockResolvedValue([
      { field_type: 'default', field_identifier: 'title', is_required: true },
    ]);
    getAllCustomFields.mockResolvedValue([]);

    const onconfirm = vi.fn().mockResolvedValue(undefined);
    render(TransitionScreenModal, {
      props: {
        show: true,
        screen: screenSummary,
        item: { id: 1, title: 'Existing title' },
        onconfirm,
      },
    });

    const titleInput = await screen.findByTestId('transition-field-title');
    await fireEvent.keyDown(titleInput, { key: 'Enter' });

    await waitFor(() =>
      expect(onconfirm).toHaveBeenCalledWith({ title: 'Existing title' })
    );
  });

  it('cancels with Escape', async () => {
    getFields.mockResolvedValue([
      { field_type: 'default', field_identifier: 'title', is_required: true },
    ]);
    getAllCustomFields.mockResolvedValue([]);

    const oncancel = vi.fn();
    render(TransitionScreenModal, {
      props: {
        show: true,
        screen: screenSummary,
        item: { id: 1, title: 'Existing title' },
        oncancel,
      },
    });

    const titleInput = await screen.findByTestId('transition-field-title');
    await fireEvent.keyDown(titleInput, { key: 'Escape' });

    await waitFor(() => expect(oncancel).toHaveBeenCalled());
  });

  it('portals the backdrop to the document body so a portaled dialog cannot cover it', async () => {
    getFields.mockResolvedValue([
      { field_type: 'default', field_identifier: 'title', is_required: true },
    ]);
    getAllCustomFields.mockResolvedValue([]);

    render(TransitionScreenModal, {
      props: {
        show: true,
        screen: screenSummary,
        item: { id: 1, title: 'Existing title' },
      },
    });

    const modal = await screen.findByTestId('transition-screen-modal');
    const backdrop = modal.closest('[role="dialog"]');
    expect(backdrop).not.toBeNull();
    expect(backdrop.parentElement).toBe(document.body);
  });
});
