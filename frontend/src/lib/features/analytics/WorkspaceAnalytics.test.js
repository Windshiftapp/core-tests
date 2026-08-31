import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    analytics: { getAnalytics: vi.fn() },
    collections: { getAll: vi.fn() },
  },
}));

vi.mock('../../router.js', () => ({
  navigate: vi.fn(),
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: vi.fn((key, params = {}) =>
    key.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? `{${name}}`))
  ),
}));

import { api } from '../../api.js';
import WorkspaceAnalytics from './WorkspaceAnalytics.svelte';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function analyticsResponse(workspaceItemNumber, title) {
  return {
    schema_version: 2,
    dataset: {
      total_items: 1,
      iteration_count: 0,
      iterations: [],
      date_from: '2026-04-25',
      date_to: '2026-07-17',
      cohort_mode: 'current_workspace',
    },
    health: {
      unfinished_items: 1,
      overdue: 0,
      stale: 0,
      unassigned: 0,
      without_priority: 0,
      without_estimate: 0,
      stale_after_days: 14,
      attention_items: [
        {
          id: workspaceItemNumber,
          workspace_item_number: workspaceItemNumber,
          title,
          status: 'Open',
          age_days: 1,
          flags: [],
        },
      ],
    },
    throughput: {
      buckets: [],
      total_created: 0,
      total_completed: 0,
      average_completed: 0,
    },
    aging_wip: {
      total_items: 0,
      buckets: [],
      by_status: [],
      oldest_items: [],
    },
    delivery_time: {
      total_items_analyzed: 0,
      trend: [],
      slowest_items: [],
      data_quality: { sufficient: false, reason: 'no_completed_items' },
    },
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  api.collections.getAll.mockResolvedValue([]);
  window.history.replaceState({}, '', '/workspaces/1/analytics');
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
});

describe('WorkspaceAnalytics analytics lifecycle', () => {
  it('does not let a previous workspace response replace the current workspace', async () => {
    const firstWorkspace = deferred();
    api.analytics.getAnalytics.mockImplementation((workspaceId) => {
      if (String(workspaceId) === '1') return firstWorkspace.promise;
      return Promise.resolve(analyticsResponse(2, 'Workspace two item'));
    });

    const view = render(WorkspaceAnalytics, { props: { workspaceId: 1 } });
    await waitFor(() =>
      expect(api.analytics.getAnalytics).toHaveBeenCalledWith(1, expect.any(Object))
    );

    await view.rerender({ workspaceId: 2 });
    expect(await screen.findByText('Workspace two item')).toBeInTheDocument();

    firstWorkspace.resolve(analyticsResponse(1, 'Stale workspace one item'));
    await Promise.resolve();
    await Promise.resolve();

    expect(screen.queryByText('Stale workspace one item')).not.toBeInTheDocument();
    expect(screen.getByText('Workspace two item')).toBeInTheDocument();
  });

  it('surfaces missing completion history when there are no delivery-time samples', async () => {
    const response = analyticsResponse(1, 'Imported completed item');
    response.delivery_time.missing_history_items = 2;
    api.analytics.getAnalytics.mockResolvedValue(response);

    render(WorkspaceAnalytics, { props: { workspaceId: 1 } });

    expect(await screen.findByText('analytics.deliveryTime.missingHistory')).toBeInTheDocument();
  });

  it('renders analytics metrics with the minimal icon treatment', async () => {
    api.analytics.getAnalytics.mockResolvedValue(analyticsResponse(1, 'Analytics item'));

    render(WorkspaceAnalytics, { props: { workspaceId: 1 } });

    const metrics = await screen.findByTestId('analytics-health-stats');
    expect(metrics.querySelectorAll('[data-appearance="minimal"]')).toHaveLength(6);
    expect(metrics.querySelectorAll('[style*="background-color"]')).toHaveLength(0);
  });
});
