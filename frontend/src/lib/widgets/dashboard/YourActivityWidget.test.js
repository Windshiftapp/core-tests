import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  activeTab: 'viewed',
  viewed: [],
  edited: [],
  commented: [],
  setActiveTab: vi.fn(),
  openItem: vi.fn(),
}));

vi.mock('../../stores/homepageStore.svelte.js', () => ({
  homepageStore: {
      get activeTab() {
        return mocks.activeTab;
      },
      get recentlyViewed() {
        return mocks.viewed;
      },
      get recentlyEdited() {
        return mocks.edited;
      },
      get recentlyCommented() {
        return mocks.commented;
      },
      loading: false,
      formatRelativeTime: () => '2 days ago',
      setActiveTab: mocks.setActiveTab,
    },
}));

vi.mock('../../stores/itemModalStore.svelte.js', () => ({
  itemModalStore: { open: mocks.openItem },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

const { default: YourActivityWidget } = await import('./YourActivityWidget.svelte');

function activityItem(overrides = {}) {
  return {
    item_id: 7,
    workspace_id: 1,
    workspace_key: 'WI',
    workspace_item_number: 7,
    title: 'The delta view',
    status: 'Open',
    status_builtin_key: 'open',
    status_color: '#3b82f6',
    priority_name: 'High',
    priority_builtin_key: 'high',
    priority_color: '#ef4444',
    last_activity: '2026-10-11T12:00:00Z',
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.activeTab = 'viewed';
  mocks.viewed.length = 0;
  mocks.edited.length = 0;
  mocks.commented.length = 0;
});

describe('Your Activity widget', () => {
  it('renders viewed activity as the shared compact work item rows', async () => {
    mocks.viewed.push(activityItem());

    render(YourActivityWidget);

    const rows = await screen.findAllByTestId('your-activity-entry');
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains('dashboard-item-row')).toBe(true);
    expect(rows[0]).toHaveAttribute('data-item-key', 'WI-7');
    expect(rows[0]).toHaveTextContent('The delta view');
    expect(rows[0]).toHaveTextContent('WI-7');
    expect(rows[0]).toHaveTextContent('2 days ago');
    expect(rows[0].querySelector('.dashboard-item-status')).not.toBeNull();

    await fireEvent.click(rows[0]);
    expect(mocks.openItem).toHaveBeenCalledWith(7, 1);
  });

  it('renders edited activity when the edited tab is active', async () => {
    mocks.activeTab = 'edited';
    mocks.edited.push(activityItem({ item_id: 9, workspace_item_number: 9, title: 'Edited item' }));

    render(YourActivityWidget);

    const rows = await screen.findAllByTestId('your-activity-entry');
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveAttribute('data-item-key', 'WI-9');
    expect(rows[0]).toHaveTextContent('Edited item');
  });

  it('requests the edited tab when its button is clicked', async () => {
    render(YourActivityWidget);

    await fireEvent.click(screen.getByText('dashboard.edited'));

    expect(mocks.setActiveTab).toHaveBeenCalledWith('edited');
  });
});
