import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('../../api/milestones.js', () => ({
  iterations: {
    getProgress: vi.fn(),
    get: vi.fn(),
    getBurndown: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getAll: vi.fn(),
    complete: vi.fn(),
  },
}));

vi.mock('../../router.js', () => ({ navigate: vi.fn() }));

vi.mock('../../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en' },
  t: (key) => key,
}));

vi.mock('../../stores/permissions.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  return {
    permissionStore: writable({ userPermissionKeys: new Set() }),
    isSystemAdmin: writable(false),
  };
});

vi.mock('../../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: {
    canAdminWorkspace: () => false,
    hasPermission: () => false,
  },
}));

vi.mock('../../stores/aiStore.svelte.js', () => ({
  aiStore: { available: false },
}));

vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));

vi.mock('./IterationBurndown.svelte', async () => ({
  default: (await import('./IterationBurndownStub.svelte')).default,
}));

import { iterations } from '../../api/milestones.js';
import IterationDetail from './IterationDetail.svelte';

const PROGRESS = {
  iteration_name: 'Sprint 1',
  description: '',
  status: 'active',
  start_date: '2026-01-01',
  end_date: '2026-01-14',
  is_global: true,
  workspace_id: null,
  total_items: 0,
  completed_items: 0,
  percent_complete: 0,
  status_breakdown: [],
  items_by_category: {},
};

const ITERATION = {
  id: 1,
  name: 'Sprint 1',
  status: 'active',
  is_global: true,
  workspace_id: null,
};

beforeEach(() => {
  vi.clearAllMocks();
  iterations.getProgress.mockResolvedValue({ ...PROGRESS });
  iterations.get.mockResolvedValue({ ...ITERATION });
  iterations.getBurndown.mockResolvedValue({ points: [] });
});

afterEach(cleanup);

test('renders the iteration header and burndown through the shared layout', async () => {
  render(IterationDetail, { iterationId: 1, workspaceId: 1 });

  expect(await screen.findByText('Sprint 1')).toBeInTheDocument();
  expect(screen.getByText('iterations.status.active')).toBeInTheDocument();
  expect(screen.getByTestId('iteration-burndown')).toHaveAttribute('data-has-data', 'true');
});

test('shows the retry action when the load fails and reloads on demand', async () => {
  iterations.getProgress.mockRejectedValueOnce(new Error('nope'));

  render(IterationDetail, { iterationId: 1, workspaceId: 1 });

  expect(await screen.findByText('nope')).toBeInTheDocument();

  await fireEvent.click(screen.getByText('common.retry'));

  await waitFor(() => expect(screen.getByText('Sprint 1')).toBeInTheDocument());
  expect(iterations.getProgress).toHaveBeenCalledTimes(2);
});
