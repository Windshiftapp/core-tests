import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/customWidgets.js', () => ({
  customWidgets: {
    get: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    generate: vi.fn(),
    data: vi.fn(),
  },
}));

vi.mock('../api/assets.js', () => ({
  assetSets: { getAll: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../stores/aiStore.svelte.js', () => ({
  aiStore: {
    available: true,
    isFeatureEnabled: () => true,
    load: vi.fn(),
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import { assetSets } from '../api/assets.js';
import { customWidgets } from '../api/customWidgets.js';
import { aiStore } from '../stores/aiStore.svelte.js';
import WidgetBuilderModal from './WidgetBuilderModal.svelte';

const program = `root = Card([\n  Title("My open work"),\n  ItemsTable("status_completed = false", "key,title,status", "-updated_at", 10)\n])`;
const queries = {
  item: { ql: 'status_completed = false', sort: '-updated_at', limit: 10 },
};

beforeAll(() => {
  // Chart observes its container, and jsdom has no ResizeObserver.
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

beforeEach(() => {
  vi.clearAllMocks();
  customWidgets.data.mockResolvedValue({ results: {} });
  aiStore.available = true;
  aiStore.isFeatureEnabled = () => true;
  assetSets.getAll.mockResolvedValue([]);
});

describe('WidgetBuilderModal', () => {
  it('generates a draft and saves it', async () => {
    customWidgets.generate.mockResolvedValue({
      name: 'My open work',
      description: 'Items assigned to me',
      program,
      queries,
    });
    customWidgets.create.mockResolvedValue({ id: 42, name: 'My open work' });
    const onsaved = vi.fn();

    render(WidgetBuilderModal, { isOpen: true, onsaved });

    await fireEvent.input(screen.getByTestId('widget-builder-prompt'), {
      target: { value: 'show my open work' },
    });
    await fireEvent.click(screen.getByTestId('widget-builder-generate'));

    await waitFor(() => {
      expect(customWidgets.generate).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(screen.getByTestId('widget-builder-name')).toHaveValue('My open work');
    });
    // The generated description must not overwrite what the user asked for.
    expect(screen.getByTestId('widget-builder-prompt')).toHaveValue('show my open work');
    expect(screen.queryByTestId('widget-builder-program')).not.toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('widget-builder-save'));

    await waitFor(() => {
      expect(customWidgets.create).toHaveBeenCalledTimes(1);
    });
    expect(customWidgets.create.mock.calls[0][0]).toMatchObject({
      name: 'My open work',
      program,
      queries,
    });
    await waitFor(() => {
      expect(onsaved).toHaveBeenCalledWith({ id: 42, name: 'My open work' });
    });
  });

  it('keeps saving disabled until there is a program', async () => {
    render(WidgetBuilderModal, { isOpen: true });

    await fireEvent.input(screen.getByTestId('widget-builder-name'), { target: { value: 'Empty' } });
    expect(screen.getByTestId('widget-builder-save')).toBeDisabled();

    customWidgets.generate.mockResolvedValue({ name: 'Empty', description: 'x', program, queries });
    await fireEvent.click(screen.getByTestId('widget-builder-generate'));
    await waitFor(() => {
      expect(screen.getByTestId('widget-builder-save')).not.toBeDisabled();
    });
  });

  it('says editing still works when generation is unavailable', async () => {
    aiStore.available = false;
    customWidgets.get.mockResolvedValue({
      id: 9,
      name: 'Existing',
      description: 'x',
      program,
      queries,
    });

    render(WidgetBuilderModal, { isOpen: true, widgetId: 9 });

    await waitFor(() => {
      expect(screen.getByTestId('widget-builder-ai-unavailable')).toHaveTextContent(
        'dashboard.widgetBuilder.aiUnavailableEditing'
      );
    });
    // An existing widget is still editable without a model: the program came
    // from the definition, so there is something to save.
    expect(screen.getByTestId('widget-builder-save')).not.toBeDisabled();
  });

  it('explains itself and cannot save when generation is unavailable', async () => {
    aiStore.available = false;

    render(WidgetBuilderModal, { isOpen: true });

    expect(screen.getByTestId('widget-builder-ai-unavailable')).toHaveTextContent(
      'dashboard.widgetBuilder.aiUnavailable'
    );
    expect(screen.queryByTestId('widget-builder-generate')).not.toBeInTheDocument();

    await fireEvent.input(screen.getByTestId('widget-builder-name'), { target: { value: 'Manual' } });
    // Without a program there is nothing to save, and there is no longer an
    // editor to type one into.
    expect(screen.getByTestId('widget-builder-save')).toBeDisabled();
  });

  it('loads an existing widget and updates it', async () => {
    customWidgets.get.mockResolvedValue({
      id: 9,
      name: 'Existing',
      description: 'x',
      program,
      queries,
    });
    customWidgets.update.mockResolvedValue({ id: 9, name: 'Existing' });
    const onsaved = vi.fn();

    render(WidgetBuilderModal, { isOpen: true, widgetId: 9, onsaved });

    await waitFor(() => {
      expect(screen.getByTestId('widget-builder-name')).toHaveValue('Existing');
    });

    await fireEvent.click(screen.getByTestId('widget-builder-save'));

    await waitFor(() => {
      expect(customWidgets.update).toHaveBeenCalledTimes(1);
    });
    expect(customWidgets.update.mock.calls[0][0]).toBe(9);
    expect(customWidgets.create).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(onsaved).toHaveBeenCalled();
    });
  });

  it('previews through the same data endpoint the dashboard uses', async () => {
    customWidgets.generate.mockResolvedValue({
      name: 'Preview',
      description: 'x',
      program,
      queries,
    });

    render(WidgetBuilderModal, { isOpen: true, workspaceId: 5 });
    await fireEvent.click(screen.getByTestId('widget-builder-generate'));

    await waitFor(() => {
      expect(customWidgets.data).toHaveBeenCalled();
    });
    const payload = customWidgets.data.mock.calls.at(-1)[0];
    expect(payload.workspace_id).toBe(5);
    expect(payload.slots).toHaveLength(1);
    expect(payload.slots[0]).toMatchObject({
      entity: 'item',
      ql: 'status_completed = false',
      sort: '-updated_at',
      limit: 10,
    });
  });

  it('resolves a component spec the prefetch did not cover', async () => {
    // The model stores one item slot (limit 10, for the table) while Metric
    // computes its own spec with the default limit, so its key never matches
    // the prefetch. Without the on-demand path the preview stays a skeleton.
    customWidgets.generate.mockResolvedValue({
      name: 'Mixed limits',
      description: 'x',
      program: `root = Card([\n  Metric("Count", "status_completed = false"),\n  ItemsTable("status_completed = false", "key,title", "", 10)\n])`,
      queries: { item: { ql: 'status_completed = false', limit: 10 } },
    });
    customWidgets.data.mockImplementation((payload) => {
      const slot = payload.slots[0];
      return Promise.resolve({
        results: { [slot.key]: { status: 'ready', rows: [], total: 3, truncated: false } },
      });
    });

    render(WidgetBuilderModal, { isOpen: true });
    await fireEvent.click(screen.getByTestId('widget-builder-generate'));

    await waitFor(() => {
      // The prefetch plus at least one on-demand fetch for the metric.
      expect(customWidgets.data.mock.calls.length).toBeGreaterThan(1);
    });
    const requestedLimits = customWidgets.data.mock.calls.map(([payload]) => payload.slots[0].limit);
    expect(requestedLimits).toContain(10);
    expect(requestedLimits.some((limit) => limit !== 10)).toBe(true);
  });

  it('previews a generated pie chart as a pie rather than a bar chart', async () => {
    const pieProgram = `root = Card([\n  PieChart("status_completed = false", "status", "", 1000, "count", "", 8)\n])`;
    const pieQueries = { item: { ql: 'status_completed = false', limit: 1000 } };
    customWidgets.generate.mockResolvedValue({
      name: 'By status',
      description: 'x',
      program: pieProgram,
      queries: pieQueries,
    });
    customWidgets.data.mockImplementation((payload) =>
      Promise.resolve({
        results: {
          [payload.slots[0].key]: {
            status: 'ready',
            rows: [
              { id: 1, status: 'Open' },
              { id: 2, status: 'Open' },
              { id: 3, status: 'In progress' },
            ],
          },
        },
      })
    );

    render(WidgetBuilderModal, { isOpen: true });
    await fireEvent.click(screen.getByTestId('widget-builder-generate'));

    await waitFor(() => {
      expect(screen.getByTestId('chart-pie-legend')).toBeInTheDocument();
    });
    const legend = screen.getByTestId('chart-pie-legend');
    expect(legend).toHaveTextContent('Open');
    expect(legend).toHaveTextContent('In progress');
    expect(screen.getByTestId('chart-slice-0')).toBeInTheDocument();
    expect(screen.getByTestId('chart-slice-1')).toBeInTheDocument();
    expect(screen.queryByTestId('chart-slice-2')).not.toBeInTheDocument();
  });
});
