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

describe('WorkItemRow SLA', () => {
  test('does not render or read SLA state on non-board rows', () => {
    renderRow();

    expect(screen.queryByTestId('work-item-sla-42')).not.toBeInTheDocument();
  });
});

describe('WorkItemRow item type icon', () => {
  test('renders the server-provided type icon when the workspace catalog omits the type', () => {
    render(WorkItemRow, {
      props: {
        item: {
          id: 77,
          workspace_id: 7,
          workspace_item_number: 20,
          title: 'Excluded type item',
          item_type_id: 1,
          item_type_name: 'Initiative',
          item_type_icon: 'Target',
          item_type_color: '#7c3aed',
        },
        // The workspace catalog intentionally omits types outside the effective
        // configuration set; the row must still render the real icon.
        itemTypes: [{ id: 2, name: 'Epic', icon: 'Zap', color: '#2563eb' }],
      },
    });

    const icon = screen.getByTestId('work-item-type-icon-77');
    expect(icon.querySelector('svg')).toHaveClass('lucide-target');
    expect(icon.querySelector('svg')).not.toHaveClass('lucide-file-text');
    expect(icon).toHaveAttribute('title', 'Initiative');
  });
});

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
