import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getMetricsAggregate: vi.fn(),
  loadWorkspaces: vi.fn(),
}));

vi.mock('../api/support.js', () => ({
  support: { getMetricsAggregate: mocks.getMetricsAggregate },
}));

vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: {
    load: mocks.loadWorkspaces,
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key),
}));

vi.mock('@lucide/svelte', () => ({
  Download: function MockDownload() {},
  LifeBuoy: function MockLifeBuoy() {},
}));

import SupportOverviewWidget from './SupportOverviewWidget.svelte';
import { support } from '../api/support.js';

const aggregateDoc = {
  summary: {
    created: 4,
    resolved: 3,
    reopened: 0,
    backlog: 6,
    unassigned: 1,
    sla: { completed: 3, breached: 0, compliant_pct: 100 },
  },
  buckets: [{ bucket_start: '2026-10-01', created: 1, resolved: 1, reopened: 0, backlog_end: 6 }],
  segments: [],
  queues: [],
};

beforeAll(() => {
  window.ResizeObserver = class MockResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
});

beforeEach(() => {
  mocks.getMetricsAggregate.mockReset();
  mocks.loadWorkspaces.mockReset();
  mocks.loadWorkspaces.mockResolvedValue([
    { id: 5, name: 'Helpdesk' },
    { id: 8, name: 'Premium Care' },
  ]);
});

describe('SupportOverviewWidget', () => {
  it('renders the setup state with workspace choices when unconfigured', async () => {
    render(SupportOverviewWidget, { config: {}, onconfigchange: null });

    const setup = await screen.findByTestId('support-overview-setup');
    expect(setup).toHaveTextContent('dashboard.states.supportOverviewSetupTitle');
    expect(screen.getByRole('button', { name: 'Helpdesk' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Premium Care' })).toBeInTheDocument();
    expect(mocks.getMetricsAggregate).not.toHaveBeenCalled();
  });

  it('requests metrics for the configured workspaces and renders the summary', async () => {
    mocks.getMetricsAggregate.mockResolvedValue(aggregateDoc);

    render(SupportOverviewWidget, { config: { workspace_ids: [5, 8] } });

    const summary = await screen.findByText('6');
    expect(summary).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();

    await waitFor(() => {
      expect(mocks.getMetricsAggregate).toHaveBeenCalledWith(
        expect.objectContaining({ workspace_ids: [5, 8], bucket: 'day' })
      );
    });
    expect(screen.queryByTestId('support-overview-setup')).not.toBeInTheDocument();
  });

  it('emits config changes when a workspace chip is toggled in the setup state', async () => {
    const onconfigchange = vi.fn();
    render(SupportOverviewWidget, { config: {}, onconfigchange });

    await fireEvent.click(await screen.findByRole('button', { name: 'Helpdesk' }));

    expect(onconfigchange).toHaveBeenCalledWith({ workspace_ids: [5] });
  });

  it('lets users add a workspace after the overview is configured', async () => {
    mocks.getMetricsAggregate.mockResolvedValue(aggregateDoc);
    const onconfigchange = vi.fn();
    render(SupportOverviewWidget, { config: { workspace_ids: [5] }, onconfigchange });

    await screen.findByText('6');
    expect(screen.queryByTestId('support-overview-workspace-picker')).not.toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('support-overview-edit-workspaces'));

    expect(screen.getByTestId('support-overview-workspace-picker')).toBeInTheDocument();
    await fireEvent.click(screen.getByTestId('support-overview-workspace-8'));
    expect(onconfigchange).toHaveBeenCalledWith({ workspace_ids: [5, 8] });
  });

  it('shows the error state when the aggregate request fails', async () => {
    mocks.getMetricsAggregate.mockRejectedValue(new Error('boom'));

    render(SupportOverviewWidget, { config: { workspace_ids: [5] } });

    expect(
      await screen.findByText('dashboard.states.supportMetricsLoadError')
    ).toBeInTheDocument();
  });
});
