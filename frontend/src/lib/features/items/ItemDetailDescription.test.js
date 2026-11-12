import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../editors/LazyMilkdownEditor.svelte', async () => ({
  default: (await import('./DescriptionLinkStub.svelte')).default,
}));

vi.mock('../../stores/attachmentStatus.svelte.js', () => ({
  attachmentStatus: { enabled: false },
}))

vi.mock('../../stores/aiStore.svelte.js', () => ({
  aiStore: { available: false },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

import ItemDetailDescription from './ItemDetailDescription.svelte';

function renderDescription(overrides = {}) {
  const onstartEditingDescription = vi.fn();
  render(ItemDetailDescription, {
    item: { id: 42, workspace_id: 7, description: 'See the [link](https://example.com/link).' },
    editingDescription: false,
    editDescription: '',
    onstartEditingDescription,
    ...overrides,
  });
  return { onstartEditingDescription };
}

describe('ItemDetailDescription click-to-edit', () => {
  it('does not start editing when a link in the read-only description is clicked', async () => {
    const { onstartEditingDescription } = renderDescription();

    await fireEvent.click(screen.getByTestId('description-link'));

    expect(onstartEditingDescription).not.toHaveBeenCalled();
    expect(screen.queryByTestId('item-description-editor')).not.toBeInTheDocument();
  });

  it('starts editing when non-link description content is clicked', async () => {
    const { onstartEditingDescription } = renderDescription();

    await fireEvent.click(screen.getByTestId('description-plain-text'));

    expect(onstartEditingDescription).toHaveBeenCalledTimes(1);
  });

  it('does not start editing when Enter is pressed on a link', async () => {
    const { onstartEditingDescription } = renderDescription();

    await fireEvent.keyDown(screen.getByTestId('description-link'), { key: 'Enter' });

    expect(onstartEditingDescription).not.toHaveBeenCalled();
  });

  it('starts editing when Enter is pressed on non-link description content', async () => {
    const { onstartEditingDescription } = renderDescription();

    await fireEvent.keyDown(screen.getByTestId('description-plain-text'), { key: 'Enter' });

    expect(onstartEditingDescription).toHaveBeenCalledTimes(1);
  });
});

describe('ItemDetailDescription attachment visibility toggle', () => {
  const attachment = {
    id: 5,
    original_filename: 'evidence.png',
    mime_type: 'image/png',
    file_size: 10,
    is_internal: false,
  };

  it('offers the toggle to an editor on a customer request', () => {
    renderDescription({
      item: { id: 42, workspace_id: 7, request_type_id: 3 },
      canEdit: true,
      attachments: [attachment],
    });

    expect(screen.getByTestId('item-attachment-visibility-5')).toBeInTheDocument();
  });

  it('offers the toggle when the requester is a portal customer', () => {
    renderDescription({
      item: { id: 42, workspace_id: 7, creator_portal_customer_id: 9 },
      canEdit: true,
      attachments: [attachment],
    });

    expect(screen.getByTestId('item-attachment-visibility-5')).toBeInTheDocument();
  });

  it('does not offer the toggle on an agent-created item', () => {
    renderDescription({
      item: { id: 42, workspace_id: 7 },
      canEdit: true,
      attachments: [attachment],
    });

    expect(screen.queryByTestId('item-attachment-visibility-5')).not.toBeInTheDocument();
  });

  it('does not offer the toggle to a read-only viewer', () => {
    renderDescription({
      item: { id: 42, workspace_id: 7, request_type_id: 3 },
      canEdit: false,
      attachments: [attachment],
    });

    expect(screen.queryByTestId('item-attachment-visibility-5')).not.toBeInTheDocument();
  });

  it('forwards the toggle to the visibility handler', async () => {
    const onattachmentToggleVisibility = vi.fn();
    renderDescription({
      item: { id: 42, workspace_id: 7, request_type_id: 3 },
      canEdit: true,
      attachments: [attachment],
      onattachmentToggleVisibility,
    });

    await fireEvent.click(screen.getByTestId('item-attachment-visibility-5'));

    expect(onattachmentToggleVisibility).toHaveBeenCalledWith(attachment);
  });
});
