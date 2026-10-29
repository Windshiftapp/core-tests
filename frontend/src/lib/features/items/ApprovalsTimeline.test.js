import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/approvals.js', () => ({
  approvals: {
        forItem: vi.fn(),
        cancel: vi.fn(),
        decide: vi.fn(),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en' },
  t: vi.fn((key, params = {}) => {
    const translations = {
      'items.approvals.requestNumber': 'Approval #{id}',
      'items.approvals.cancelRequest': 'Cancel request',
      'items.approvals.noEligibleApprovers': 'No eligible approvers were found.',
      'items.approvals.selfApprovalHint':
        'You opened this request. If you are also the configured approver, self-approval may be disabled.',
      'items.approvals.approverSourceHint': 'Check the approver source for this step.',
    };
    let value = translations[key] ?? key;
    for (const [name, replacement] of Object.entries(params)) {
      value = value.replace(`{${name}}`, String(replacement));
    }
    return value;
  }),
}));
vi.mock('../../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));
vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));
vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { id: 42 } },
}));

import { approvals } from '../../api/approvals.js';
import ApprovalsTimeline from './ApprovalsTimeline.svelte';

const pendingRequest = {
  id: 7,
  status: 'pending',
  triggered_by_user_id: 99,
  created_at: '2026-07-17T17:07:00Z',
  step_instances: [],
  decisions: [],
};

beforeEach(() => {
  vi.clearAllMocks();
  approvals.forItem.mockResolvedValue([pendingRequest]);
});

describe('ApprovalsTimeline cancellation', () => {
  test('renders an existing approval list without fetching it again', async () => {
    render(ApprovalsTimeline, {
      itemId: 12,
      canCancel: true,
      initialRequests: [pendingRequest],
    });

    expect(await screen.findByText('Approval #7')).toBeTruthy();
    expect(approvals.forItem).not.toHaveBeenCalled();
  });

  test('shows cancellation to an item editor when another user opened the request', async () => {
    render(ApprovalsTimeline, { itemId: 12, canCancel: true });

    expect(await screen.findByText('Cancel request')).toBeTruthy();
  });

  test('does not show cancellation to a non-requestor without item edit permission', async () => {
    render(ApprovalsTimeline, { itemId: 12 });

    await screen.findByText('Approval #7');
    expect(screen.queryByText('Cancel request')).toBeNull();
  });

  test('explains an empty pool when the requestor may have been excluded from self-approval', async () => {
    approvals.forItem.mockResolvedValue([
      {
        ...pendingRequest,
        triggered_by_user_id: 42,
        step_instances: [
          {
            id: 8,
            display_order: 0,
            status: 'pending',
            started_at: '2026-07-17T17:07:00Z',
            approvers: [],
          },
        ],
      },
    ]);

    render(ApprovalsTimeline, { itemId: 12 });

    expect(await screen.findByText('No eligible approvers were found.')).toBeTruthy();
    expect(screen.getByText(/self-approval may be disabled/)).toBeTruthy();
  });
});

describe('ApprovalsTimeline decision visibility', () => {
  test('keeps decision comments isolated by request and clears only the submitted comment', async () => {
    const requests = [7, 8].map((id) => ({
      ...pendingRequest,
      id,
      step_instances: [
        {
          id: id + 10,
          display_order: 0,
          status: 'pending',
          started_at: '2026-07-17T17:07:00Z',
          approvers: [{ user_id: 42, is_active: true }],
        },
      ],
    }));
    approvals.forItem.mockResolvedValue(requests);

    render(ApprovalsTimeline, {
      itemId: 12,
      initialRequests: requests,
    });

    const firstRequest = within(screen.getByTestId('approval-request-7'));
    const secondRequest = within(screen.getByTestId('approval-request-8'));
    const firstComment = firstRequest.getByTestId('approval-decision-comment');
    const secondComment = secondRequest.getByTestId('approval-decision-comment');

    await fireEvent.input(firstComment, { target: { value: 'Keep this draft' } });
    expect(firstComment.value).toBe('Keep this draft');
    expect(secondComment.value).toBe('');

    await fireEvent.input(secondComment, { target: { value: 'Submit this comment' } });
    await fireEvent.click(secondRequest.getByTestId('approval-decision-comment-submit'));

    await waitFor(() => {
      expect(approvals.decide).toHaveBeenCalledWith(8, 'comment', 'Submit this comment');
      expect(
        within(screen.getByTestId('approval-request-7')).getByTestId('approval-decision-comment')
          .value
      ).toBe('Keep this draft');
      expect(
        within(screen.getByTestId('approval-request-8')).getByTestId('approval-decision-comment')
          .value
      ).toBe('');
    });
  });

  test('shows decision controls when the current user belongs to a later parallel step', async () => {
    render(ApprovalsTimeline, {
      itemId: 12,
      initialRequests: [
        {
          ...pendingRequest,
          step_instances: [
            {
              id: 8,
              display_order: 0,
              status: 'pending',
              started_at: '2026-07-17T17:07:00Z',
              approvers: [{ user_id: 7, is_active: true }],
            },
            {
              id: 9,
              display_order: 1,
              status: 'pending',
              started_at: '2026-07-17T17:07:00Z',
              approvers: [{ user_id: 42, is_active: true }],
            },
          ],
        },
      ],
    });

    expect(await screen.findByTestId('approval-decision-approve')).toBeTruthy();
    expect(screen.getByTestId('approval-decision-reject')).toBeTruthy();
  });

  test('hides decision controls when the current user has no active started step', async () => {
    render(ApprovalsTimeline, {
      itemId: 12,
      initialRequests: [
        {
          ...pendingRequest,
          step_instances: [
            {
              id: 8,
              display_order: 0,
              status: 'pending',
              started_at: '2026-07-17T17:07:00Z',
              approvers: [{ user_id: 42, is_active: false }],
            },
            {
              id: 9,
              display_order: 1,
              status: 'pending',
              started_at: null,
              approvers: [{ user_id: 42, is_active: true }],
            },
          ],
        },
      ],
    });

    await screen.findByText('Approval #7');
    expect(screen.queryByTestId('approval-decision-approve')).toBeNull();
    expect(screen.queryByTestId('approval-decision-reject')).toBeNull();
  });
});
