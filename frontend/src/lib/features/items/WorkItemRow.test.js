import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) => {
    const translations = {
      'items.storyPoints': 'Story Points',
      'items.setField': `Set ${params.field}`,
      'items.notSet': 'Not set',
      'items.enterField': `Enter ${params.field}`,
    };
    return translations[key] ?? key;
  },
}));

const { default: WorkItemRow } = await import('./WorkItemRow.svelte');

afterEach(() => cleanup());

function renderRow(props = {}) {
  const item = {
    id: 42,
    workspace_id: 7,
    workspace_item_number: 12,
    title: 'Estimate the backlog item',
    story_points: null,
  };
  const onOpenItem = vi.fn();
  const onStoryPointsChange = vi.fn();

  render(WorkItemRow, {
    props: {
      item,
      showStoryPoints: true,
      onclick: onOpenItem,
      onStoryPointsChange,
      ...props,
    },
  });

  return { item, onOpenItem, onStoryPointsChange };
}

describe('WorkItemRow story points', () => {
  test('does not render when the backlog screen does not expose the field', () => {
    renderRow({ showStoryPoints: false });

    expect(screen.queryByTestId('backlog-story-points-42')).not.toBeInTheDocument();
  });

  test('edits the configured field without opening the item row', async () => {
    const { onOpenItem, onStoryPointsChange } = renderRow();

    await fireEvent.click(screen.getByTestId('backlog-story-points-button-42'));
    const input = screen.getByTestId('backlog-story-points-input-42');
    await fireEvent.input(input, { target: { value: '3' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onStoryPointsChange).toHaveBeenCalledWith(3);
    expect(onOpenItem).not.toHaveBeenCalled();
  });
});
