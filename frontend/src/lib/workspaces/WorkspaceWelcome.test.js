import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// WI-1932: the workspace dashboard is the second consumer of
// useDashboardCustomization. These tests cover the adapters it injects, which
// the composable's own unit tests cannot see.

const mocks = vi.hoisted(() => ({
  loadWorkspaceGradient: vi.fn(),
}));

vi.mock('../stores/workspaceGradient.svelte.js', () => ({
  useGradientStyles: () => ({ glassTextStyle: '', glassSubtleTextStyle: '' }),
  loadWorkspaceGradient: mocks.loadWorkspaceGradient,
  hydrateWorkspaceGradientLayout: vi.fn(),
  workspaceGradientIndex: writable(null),
  applyToAllViews: writable(false),
  workspaceBackgroundImageUrl: writable(''),
}));

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
    get: vi.fn().mockResolvedValue({ id: 7, name: 'Workspace', key: 'WS' }),
    getStats: vi.fn().mockResolvedValue({
      totalCollections: 0,
      itemsByStatusCategory: {},
      totalItems: 0,
    }),
    updateHomepageLayout: vi.fn().mockResolvedValue({}),
  },
}));

vi.mock('../api/workflows.js', () => ({
  statusCategories: { getAll: vi.fn().mockResolvedValue([]) },
  statuses: { getAll: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../api/items.js', () => ({
  items: { getAll: vi.fn().mockResolvedValue({ items: [], total: 0 }) },
}));

vi.mock('../features/collections/collectionService.js', () => ({
  getCollection: vi.fn().mockResolvedValue(null),
}));

vi.mock('./WorkspaceCustomizationSidebar.svelte', () => ({
  default: function WorkspaceCustomizationSidebar() {},
}));

vi.mock('../widgets/WidgetWrapper.svelte', () => ({
  default: function WidgetWrapper() {},
}));

const { default: WorkspaceWelcome } = await import('./WorkspaceWelcome.svelte');
const { workspaces } = await import('../api/workspaces.js');

const layout = () => ({
  sections: [
    { id: 's1', title: 'Alpha', subtitle: '', display_order: 0, widget_ids: [] },
    { id: 's2', title: 'Beta', subtitle: '', display_order: 1, widget_ids: [] },
  ],
  widgets: [],
  gradient: null,
  applyToAllViews: false,
  backgroundImageUrl: '',
});

beforeEach(() => {
  vi.clearAllMocks();
  mocks.loadWorkspaceGradient.mockResolvedValue(layout());
});

// The component debounces layout saves by 1s, and runed rejects a pending
// debounce with "Cancelled" when the component unmounts. Every test that leaves
// a pending save must therefore wait for it before finishing.
async function waitForPersistedSave() {
  await waitFor(() => expect(workspaces.updateHomepageLayout).toHaveBeenCalled(), {
    timeout: 3000,
  });
  return workspaces.updateHomepageLayout.mock.calls.at(-1)[1];
}

describe('WorkspaceWelcome dashboard section editing', () => {
  it('renames a section through the shared state machine and persists it', async () => {
    render(WorkspaceWelcome, { props: { workspaceId: 7 } });

    await waitFor(() => expect(screen.getByText('Alpha')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('workspace-dashboard-edit-toggle'));

    await fireEvent.click(screen.getAllByTestId('workspace-section-edit')[0]);

    const titleInput = await screen.findByTestId('workspace-section-title-input');
    await fireEvent.input(titleInput, { target: { value: 'Renamed' } });
    await fireEvent.keyDown(titleInput, { key: 'Enter' });

    await waitFor(() => expect(screen.getByText('Renamed')).toBeInTheDocument());
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument();

    // The injected updateSection adapter must also persist the layout, not just
    // update the local sections array.
    const savedLayout = await waitForPersistedSave();
    expect(savedLayout.sections.find((section) => section.id === 's1').title).toBe('Renamed');
  });

  it('keeps a new section when the title is set and discards it when cleared', async () => {
    render(WorkspaceWelcome, { props: { workspaceId: 7 } });

    await waitFor(() => expect(screen.getByText('Alpha')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('workspace-dashboard-edit-toggle'));

    // A new section starts with a non-empty default title, so clearing it is
    // what makes leaving edit mode discard the section.
    await fireEvent.click(screen.getByTestId('workspace-add-section'));
    const clearedInput = await screen.findByTestId('workspace-section-title-input');
    await fireEvent.input(clearedInput, { target: { value: '   ' } });
    expect(screen.getAllByTestId('workspace-section')).toHaveLength(3);

    await fireEvent.click(screen.getByTestId('workspace-dashboard-edit-toggle'));
    await waitFor(() => expect(screen.getAllByTestId('workspace-section')).toHaveLength(2));

    // A typed title keeps the section instead.
    await fireEvent.click(screen.getByTestId('workspace-dashboard-edit-toggle'));
    await fireEvent.click(screen.getByTestId('workspace-add-section'));
    const titledInput = await screen.findByTestId('workspace-section-title-input');
    await fireEvent.input(titledInput, { target: { value: 'Kept' } });

    await fireEvent.click(screen.getByTestId('workspace-dashboard-edit-toggle'));
    await waitFor(() => expect(screen.getByText('Kept')).toBeInTheDocument());
    expect(screen.getAllByTestId('workspace-section')).toHaveLength(3);

    const savedLayout = await waitForPersistedSave();
    expect(savedLayout.sections.some((section) => section.title === 'Kept')).toBe(true);
  });

  it('discards a new section on Escape', async () => {
    render(WorkspaceWelcome, { props: { workspaceId: 7 } });

    await waitFor(() => expect(screen.getByText('Alpha')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('workspace-dashboard-edit-toggle'));
    await fireEvent.click(screen.getByTestId('workspace-add-section'));

    const titleInput = await screen.findByTestId('workspace-section-title-input');
    await fireEvent.keyDown(titleInput, { key: 'Escape' });

    await waitFor(() => expect(screen.getAllByTestId('workspace-section')).toHaveLength(2));
  });
});
