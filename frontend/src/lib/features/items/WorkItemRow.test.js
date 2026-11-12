import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en' },
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

describe('WorkItemRow configurable backlog metadata', () => {
  const childItem = {
    id: 42,
    workspace_id: 7,
    workspace_key: 'CHILD',
    workspace_item_number: 12,
    title: 'Implement the final step',
    parent_id: 7,
    parent_key: 'EPIC-7',
    parent_title: 'Release the new workflow',
  };

  test('shows the permission-checked parent key and title when requested', () => {
    render(WorkItemRow, { props: { item: childItem, showParent: true } });

    const parent = screen.getByTestId('work-item-parent-42');
    expect(screen.getByTestId('work-item-parent-container-42')).toHaveStyle({
      maxWidth: 'clamp(6rem, 20%, 16rem)',
    });
    expect(parent).toHaveTextContent('EPIC-7');
    expect(parent).toHaveTextContent('Release the new workflow');
    // The row must not derive a key from the child's own workspace.
    expect(parent).not.toHaveTextContent('CHILD-7');
  });

  test('keeps parent metadata hidden in rows that do not opt in', () => {
    render(WorkItemRow, { props: { item: childItem } });

    expect(screen.queryByTestId('work-item-parent-42')).not.toBeInTheDocument();
  });

  test('does not expose a raw parent database id when summary data is unavailable', () => {
    render(WorkItemRow, {
      props: {
        item: {
          ...childItem,
          parent_key: '',
          parent_title: '',
          parent_workspace_item_number: null,
        },
        showParent: true,
      },
    });

    expect(screen.queryByTestId('work-item-parent-42')).not.toBeInTheDocument();
  });

  test('shows the selected backlog metadata', () => {
    render(WorkItemRow, {
      props: {
        item: {
          ...childItem,
          priority_name: 'High',
          priority_color: '#ef4444',
          assignee_name: 'Ada Lovelace',
          due_date: '2026-11-20',
          milestones: [{ id: 3, name: 'Autumn release', category_color: '#8b5cf6' }],
        },
        showPriority: true,
        showAssignee: true,
        showDueDate: true,
        showMilestones: true,
      },
    });

    expect(screen.getByTestId('work-item-priority-42')).toHaveTextContent('High');
    expect(screen.getByTestId('work-item-assignee-42')).toHaveTextContent('Ada Lovelace');
    expect(screen.getByTestId('work-item-due-date-42')).toBeInTheDocument();
    expect(screen.getByTestId('work-item-milestone-42')).toHaveTextContent('Autumn release');
  });
});
