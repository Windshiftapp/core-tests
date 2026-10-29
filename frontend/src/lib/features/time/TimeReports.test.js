import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  aggregate: vi.fn(),
  getPage: vi.fn(),
  getProjects: vi.fn(),
  getCustomers: vi.fn(),
}));

vi.mock('../../api/portal.js', () => ({
  customerOrganisations: { getAll: mocks.getCustomers },
}))

vi.mock('../../api/time.js', () => ({
  time: {
        projects: { getAll: mocks.getProjects },
        worklogs: { aggregate: mocks.aggregate, getPage: mocks.getPage },
      },
}));

vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: { currentUser: null },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import TimeReports from './TimeReports.svelte';
import { customerOrganisations } from '../../api/portal.js';
import { time } from '../../api/time.js';


function worklog(id) {
  const start = 1_767_225_600 + id * 3600; // deterministic unix seconds
  return {
    id,
    user_id: 1,
    user_name: 'Agent',
    customer_name: 'Customer',
    project_name: 'Project',
    description: `entry-${id}`,
    start_time: start,
    end_time: start + 1800,
    duration_minutes: 30,
  };
}

let originalCreateObjectURL;
let originalRevokeObjectURL;
let capturedBlob;

beforeEach(() => {
  vi.clearAllMocks();
  mocks.getProjects.mockResolvedValue([]);
  mocks.getCustomers.mockResolvedValue([]);
  mocks.aggregate.mockResolvedValue({ totals: { duration_ms: 1_800_000, entry_count: 1 } });
  mocks.getPage.mockResolvedValue({
    data: [worklog(1)],
    pagination: { total: 1, total_pages: 1 },
  });

  capturedBlob = null;
  originalCreateObjectURL = URL.createObjectURL;
  originalRevokeObjectURL = URL.revokeObjectURL;
  URL.createObjectURL = vi.fn((blob) => {
    capturedBlob = blob;
    return 'blob:mock';
  });
  URL.revokeObjectURL = vi.fn();
});

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL;
  URL.revokeObjectURL = originalRevokeObjectURL;
  document.body.innerHTML = '';
});

describe('TimeReports', () => {
  it('does not refetch or clear the report when a date bound is cleared (WI-1584)', async () => {
    render(TimeReports, { props: { workspaceId: 1 } });

    await waitFor(() => expect(mocks.getPage).toHaveBeenCalledTimes(1));
    const pageCalls = mocks.getPage.mock.calls.length;
    const aggregateCalls = mocks.aggregate.mock.calls.length;

    await fireEvent.input(document.querySelector('#report-date-from'), { target: { value: '' } });
    await fireEvent.click(screen.getByTestId('time-report-apply-filters'));

    // The incomplete range must not issue a rejected query that resets the
    // report to an empty/zero state.
    await Promise.resolve();
    expect(mocks.getPage.mock.calls.length).toBe(pageCalls);
    expect(mocks.aggregate.mock.calls.length).toBe(aggregateCalls);
  });

  it('exports every personal worklog exactly once across pagination (WI-1583)', async () => {
    const firstPage = Array.from({ length: 100 }, (_, i) => worklog(i + 1));
    const exportPage = Array.from({ length: 150 }, (_, i) => worklog(i + 1));
    mocks.getPage.mockImplementation((filters) => {
      if (filters.page_size === 100) {
        return Promise.resolve({ data: firstPage, pagination: { total: 150, total_pages: 2 } });
      }
      return Promise.resolve({ data: exportPage, pagination: { total: 150, total_pages: 1 } });
    });

    render(TimeReports, { props: { workspaceId: 1 } });
    await waitFor(() =>
      expect(mocks.getPage).toHaveBeenCalledWith(expect.objectContaining({ page_size: 100 }))
    );

    await fireEvent.click(screen.getByTestId('time-report-export-csv'));
    await waitFor(() => expect(capturedBlob).not.toBeNull());

    const csv = await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result);
      reader.onerror = () => reject(reader.error);
      reader.readAsText(capturedBlob);
    });
    const entryRows = csv.split('\n').filter((line) => line.includes('entry-'));
    expect(entryRows).toHaveLength(150);
    expect(new Set(entryRows).size).toBe(150);
  });
});
