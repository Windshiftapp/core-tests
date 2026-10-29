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
