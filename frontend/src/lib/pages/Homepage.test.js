import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  getLayout: vi.fn(),
  updateLayout: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    homepage: mocks,
  },
}));

vi.mock('../stores/i18n.svelte.js', async (importOriginal) => ({
  ...(await importOriginal()),
  t: (key) =>
    ({
      'layout.moveUp': 'Move section up',
      'layout.moveDown': 'Move section down',
    })[key] ?? key,
}));

vi.mock('./DashboardOnboarding.svelte', () => ({ default: function DashboardOnboarding() {} }));
vi.mock('../layout/DashboardCustomizationSidebar.svelte', () => ({
  default: function DashboardCustomizationSidebar() {},
}));

const { homepageStore } = await import('../stores/homepageStore.svelte.js');
const { default: Homepage } = await import('./Homepage.svelte');

function homepageResponse() {
  return {
    recent_workspaces: [],
    total_workspace_count: 2,
    total_item_count: 8,
    recently_viewed: [],
    recently_edited: [],
    recently_commented: [],
    watched_items: [],
    upcoming_milestones: [],
    layout: {
      grid_columns: 12,
      sections: [
        { id: 'first', title: 'First', subtitle: '', display_order: 0, widget_ids: [] },
        { id: 'second', title: 'Second', subtitle: '', display_order: 1, widget_ids: [] },
      ],
      widgets: [],
    },
    layout_revision: 'sha256:layout-v1',
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  homepageStore.reset();
  localStorage.setItem('windshift-dashboard-onboarding-dismissed', 'true');
  mocks.get.mockResolvedValue(homepageResponse());
  mocks.updateLayout.mockResolvedValue({ sections: [], widgets: [] });
  Element.prototype.scrollIntoView = vi.fn();
});

describe('Homepage dashboard sections', () => {
  it('focuses and scrolls a newly added section into view', async () => {
    render(Homepage);

    await waitFor(() => expect(homepageStore.layoutLoaded).toBe(true));
    await fireEvent.click(screen.getByTestId('dashboard-edit-toggle'));
    await fireEvent.click(screen.getByTestId('dashboard-add-section'));

    const titleInput = await screen.findByTestId('dashboard-section-title-input');
    await waitFor(() => expect(titleInput).toHaveFocus());
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center',
    });
  });

  it('reorders sections with the edit controls', async () => {
    render(Homepage);

    await waitFor(() => expect(homepageStore.layoutLoaded).toBe(true));
    await fireEvent.click(screen.getByTestId('dashboard-edit-toggle'));

    await fireEvent.click(screen.getAllByTestId('dashboard-section-move-up')[1]);

    const sections = screen.getAllByTestId('dashboard-section');
    expect(sections[0]).toHaveTextContent('Second');
    expect(sections[1]).toHaveTextContent('First');
  });
});
