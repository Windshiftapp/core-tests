import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getMetricsAggregate: vi.fn(),
}));

vi.mock('../api/support.js', () => ({
  support: { getMetricsAggregate: mocks.getMetricsAggregate },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('@lucide/svelte', () => ({
  Download: function MockDownload() {},
}));

import SupportMetricsWidget from './SupportMetricsWidget.svelte';
import { support } from '../api/support.js';

beforeAll(() => {
  window.ResizeObserver = class MockResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
});


const aggregateDoc = {
  from: '2026-09-05',
  to: '2026-10-02',
  timezone: 'UTC',
  bucket: 'day',
  summary: {
    created: 12,
    resolved: 9,
    reopened: 2,
    backlog: 7,
    unassigned: 3,
    first_response: { p50_ms: 5400000, p90_ms: 18000000, count: 10 },
    resolution: { p50_ms: 86400000, p90_ms: 259200000, count: 9 },
    sla: { completed: 8, breached: 2, compliant_pct: 75 },
  },
  buckets: [
    { bucket_start: '2026-10-01', created: 2, resolved: 1, reopened: 0, backlog_end: 7 },
    { bucket_start: '2026-10-02', created: 3, resolved: 2, reopened: 1, backlog_end: 7 },
  ],
  segments: [],
  queues: [],
};

const realCreateObjectURL = URL.createObjectURL;
const realAnchorClick = HTMLAnchorElement.prototype.click;
const realRevokeObjectURL = URL.revokeObjectURL;

beforeEach(() => {
  mocks.getMetricsAggregate.mockReset();
  URL.createObjectURL = vi.fn(() => 'blob:mock');
  URL.revokeObjectURL = vi.fn();
  HTMLAnchorElement.prototype.click = vi.fn();
  return () => {
    URL.createObjectURL = realCreateObjectURL;
    URL.revokeObjectURL = realRevokeObjectURL;
    HTMLAnchorElement.prototype.click = realAnchorClick;
  };
});

describe('SupportMetricsWidget', () => {
  it('renders the summary, trend, and export from the aggregate document', async () => {
    mocks.getMetricsAggregate.mockResolvedValue(aggregateDoc);

    render(SupportMetricsWidget, { workspaceId: 5 });

    await waitFor(() => {
      expect(mocks.getMetricsAggregate).toHaveBeenCalledTimes(1);
    });

    const summary = await screen.findByTestId('support-metrics-summary');
    expect(summary).toHaveTextContent('7');
    expect(summary).toHaveTextContent('3');
    expect(summary).toHaveTextContent('12');
    expect(summary).toHaveTextContent('9');
    expect(summary).toHaveTextContent('75%');

    expect(mocks.getMetricsAggregate.mock.calls[0][0].from).toMatch(/^\d{4}-\d{2}-\d{2}$/);

    const call = mocks.getMetricsAggregate.mock.calls[0][0];
    expect(call.workspace_id).toBe(5);
    expect(call.collection_id).toBeUndefined();
    expect(call.bucket).toBe('day');
  });

  it('scopes by collection when rendered on a collection homepage', async () => {
    mocks.getMetricsAggregate.mockResolvedValue(aggregateDoc);

    render(SupportMetricsWidget, { workspaceId: 5, collectionId: 42, config: { window: '7d' } });

    await waitFor(() => {
      expect(mocks.getMetricsAggregate).toHaveBeenCalledTimes(1);
    });
    const call = mocks.getMetricsAggregate.mock.calls[0][0];
    expect(call.collection_id).toBe(42);
    expect(call.workspace_id).toBeUndefined();

    const from = call.from;
    const to = call.to;
    const days = Math.round(
      (new Date(`${to}T00:00:00Z`) - new Date(`${from}T00:00:00Z`)) / 86400000
    );
    expect(days).toBe(6);
  });

  it('shows the explicit empty state when the scope has no tickets', async () => {
    mocks.getMetricsAggregate.mockResolvedValue({
      ...aggregateDoc,
      summary: { ...aggregateDoc.summary, created: 0, resolved: 0, backlog: 0 },
    });

    render(SupportMetricsWidget, { workspaceId: 5 });

    expect(await screen.findByText('dashboard.states.supportMetricsEmpty')).toBeInTheDocument();
    expect(screen.queryByTestId('support-metrics-summary')).not.toBeInTheDocument();
  });

  it('shows the error state when the aggregate request fails', async () => {
    mocks.getMetricsAggregate.mockRejectedValue(new Error('boom'));

    render(SupportMetricsWidget, { workspaceId: 5 });

    expect(
      await screen.findByText('dashboard.states.supportMetricsLoadError')
    ).toBeInTheDocument();
  });

  it('exports the bucket rows as CSV with the stable column headers', async () => {
    mocks.getMetricsAggregate.mockResolvedValue(aggregateDoc);

    render(SupportMetricsWidget, { workspaceId: 5 });
    await screen.findByTestId('support-metrics-summary');

    await fireEvent.click(screen.getByTestId('support-metrics-export'));

    await waitFor(() => {
      expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
    });
    expect(URL.createObjectURL.mock.calls[0][0].type).toBe('text/csv;charset=utf-8');
  });

  it('serializes bucket rows with the stable column headers', async () => {
    const { supportMetricsToCSV } = await import('../features/support/metricColumns.js');
    const csv = supportMetricsToCSV(aggregateDoc);
    const lines = csv.split('\n');
    expect(lines[0]).toBe(
      'Date,Created,Resolved,Reopened,Backlog at end,First response p50 (ms),Resolution p50 (ms)'
    );
    expect(lines[1]).toBe('2026-10-01,2,1,0,7,,');
    expect(lines[2]).toBe('2026-10-02,3,2,1,7,,');
  });
});
