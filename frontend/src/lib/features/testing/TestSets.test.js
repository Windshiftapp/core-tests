import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, expect, test, vi } from 'vitest';

vi.mock('../../api/milestones.js', () => ({
  milestones: { getAll: vi.fn().mockResolvedValue([]) },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../../api/tests/index.js', () => ({
  tests: {
        testPlans: {
          getAll: vi.fn().mockResolvedValue([
            {
              id: 1,
              name: 'First Test Plan',
              description: 'Risk-prioritized manual verification',
              milestone_name: null,
              test_case_count: 57,
              total_runs: 1,
              successful_runs: 0,
              failed_runs: 1,
              last_run_status: 'failed',
              last_run_date: '2026-07-17T10:00:00Z',
            },
            {
              id: 2,
              name: 'Milestone Plan',
              description: '',
              milestone_name: 'Release 1',
              test_case_count: 3,
              total_runs: 3,
              successful_runs: 2,
              failed_runs: 1,
              last_run_status: 'passed',
              last_run_date: '2026-07-18T10:00:00Z',
            },
          ]),
        },
      },
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en' },
  t: (key) =>
    ({
      'common.id': 'ID',
      'common.name': 'Name',
      'common.actions': 'Actions',
      'common.total': 'Total',
      'milestones.milestone': 'Milestone',
      'testing.testCases': 'Test Cases',
      'testing.testRuns': 'Test Runs',
      'testing.lastRun': 'Last Run',
      'testing.noMilestone': 'No Milestone',
      'testing.neverRun': 'Never run',
      'testing.testPlans': 'Test Plans',
      'testing.testPlansSubtitle': 'Plans',
    })[key] ?? key,
}));

import TestSets from './TestSets.svelte';
import { milestones } from '../../api/milestones.js';
import { tests } from '../../api/tests/index.js';


afterEach(cleanup);

test('renders run statistics, milestone, and last-run status as standard badges', async () => {
  render(TestSets, { workspaceId: 7 });

  const firstRow = await screen.findByTestId('test-set-row-1');
  expect(within(firstRow).getByText('1 total')).toBeInTheDocument();
  expect(within(firstRow).getByText('0 ✓')).toBeInTheDocument();
  expect(within(firstRow).getByText('1 ✗')).toBeInTheDocument();
  expect(within(firstRow).getByText('No Milestone')).toBeInTheDocument();
  expect(within(firstRow).getByText('Failed')).toBeInTheDocument();

  const secondRow = screen.getByTestId('test-set-row-2');
  expect(within(secondRow).getByText('Release 1')).toBeInTheDocument();
  expect(within(secondRow).getByText('2 ✓')).toBeInTheDocument();
  expect(within(secondRow).getByText('Passed')).toBeInTheDocument();
});
