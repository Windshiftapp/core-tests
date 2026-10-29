import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('../../api/milestones.js', () => ({
  milestones: {
    getProgress: vi.fn(),
    get: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
  },
}));

vi.mock('../../router.js', () => ({ navigate: vi.fn() }));

vi.mock('../../stores/toasts.svelte.js', () => ({
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

vi.mock('../../stores/milestones.js', () => ({
  milestonesStore: { update: vi.fn(), delete: vi.fn() },
}));

vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));

import { milestones } from '../../api/milestones.js';
import { formatDateShort } from '../../utils/dateFormatter.js';
import MilestoneDetail from './MilestoneDetail.svelte';

const PROGRESS = {
  milestone_name: 'Release 1',
  description: '',
  status: 'completed',
  target_date: null,
  is_global: false,
  workspace_id: 7,
  total_items: 0,
  completed_items: 0,
  percent_complete: 0,
  status_breakdown: [],
  items_by_category: {},
};

beforeEach(() => {
  vi.clearAllMocks();
  milestones.getProgress.mockResolvedValue({ ...PROGRESS });
});

afterEach(cleanup);

test('shows the release date for a released release', async () => {
  milestones.get.mockResolvedValue({
    releases: [
      {
        id: 2,
        tag_name: 'v0.9.0',
        name: 'v0.9.0',
        released_at: '2025-12-15T12:00:00Z',
        created_at: '2025-12-01T12:00:00Z',
        release_status: 'released',
        is_draft: false,
        is_prerelease: false,
      },
    ],
  });

  render(MilestoneDetail, { milestoneId: 1 });

  const row = await screen.findByTestId('milestone-release-2');
  expect(within(row).getByText(formatDateShort('2025-12-15T12:00:00Z'))).toBeInTheDocument();
});

test('does not show the record creation time for an unreleased release', async () => {
  milestones.get.mockResolvedValue({
    releases: [
      {
        id: 1,
        tag_name: 'v1.0.0',
        name: 'v1.0.0',
        released_at: null,
        created_at: '2026-01-01T12:00:00Z',
        release_status: 'draft',
        is_draft: true,
        is_prerelease: false,
      },
    ],
  });

  render(MilestoneDetail, { milestoneId: 1 });

  const row = await screen.findByTestId('milestone-release-1');
  expect(row).toHaveTextContent('v1.0.0');
  expect(within(row).queryByText(formatDateShort('2026-01-01T12:00:00Z'))).not.toBeInTheDocument();
});
