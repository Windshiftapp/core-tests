import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { getEmailLog, requeueRateLimitedEmail, getEmailReplies, retryEmailReply, discardEmailReply } = vi.hoisted(() => ({
  getEmailLog: vi.fn(),
  requeueRateLimitedEmail: vi.fn(),
  getEmailReplies: vi.fn(),
  retryEmailReply: vi.fn(),
  discardEmailReply: vi.fn(),
}));

vi.mock('../api/channels.js', () => ({
  channels: {
        getEmailLog,
        requeueRateLimitedEmail,
        getEmailReplies,
        retryEmailReply,
        discardEmailReply,
      },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params) =>
    [key, ...(params && typeof params === 'object' ? Object.values(params) : [])].join(' '),
}));

import EmailLogModal from './EmailLogModal.svelte';
import { channels } from '../api/channels.js';

const channel = { id: 7, name: 'Support', config: '{}' };

const rateLimitedLog = {
  state: { last_checked_at: null, last_uid: 2, error_count: 0, last_error: '', rate_limited_count: 1 },
  messages: [
    {
      id: 11,
      from_email: 'flooder@example.com',
      from_name: 'Flooder',
      subject: 'Flood',
      item_id: null,
      comment_id: null,
      processed_at: '2026-09-23T10:00:00Z',
      rate_limited_at: '2026-09-23T10:00:00Z',
    },
    {
      id: 10,
      from_email: 'calm@example.com',
      from_name: 'Calm',
      subject: 'Normal request',
      item_id: 42,
      comment_id: null,
      processed_at: '2026-09-23T09:00:00Z',
    },
  ],
  total: 2,
  page: 1,
  page_size: 50,
};

function renderModal() {
  render(EmailLogModal, { props: { isOpen: true, channel, onClose: () => {} } });
}

afterEach(() => {
  cleanup();
  getEmailLog.mockReset();
  requeueRateLimitedEmail.mockReset();
  getEmailReplies.mockReset();
  retryEmailReply.mockReset();
  discardEmailReply.mockReset();
});

describe('EmailLogModal', () => {
  it('shows the rate-limited badge and recovery banner, and requeueing reloads the log', async () => {
    getEmailLog.mockResolvedValue(structuredClone(rateLimitedLog));
    requeueRateLimitedEmail.mockResolvedValue({ requeued: true, from_uid: 2 });
    renderModal();

    await waitFor(() => expect(screen.getByTestId('email-log-rate-limited-badge')).toBeInTheDocument());
    const banner = screen.getByTestId('email-log-rate-limited-banner');
    expect(banner).toBeInTheDocument();
    expect(banner).toHaveTextContent('1');

    await fireEvent.click(screen.getByTestId('email-log-requeue-button'));

    await waitFor(() => expect(requeueRateLimitedEmail).toHaveBeenCalledWith(7));
    await waitFor(() => expect(getEmailLog).toHaveBeenCalledTimes(2));
  });

  it('hides the recovery banner when nothing is rate-limited', async () => {
    const log = structuredClone(rateLimitedLog);
    log.state.rate_limited_count = 0;
    log.messages = log.messages.filter((m) => !m.rate_limited_at);
    log.total = 1;
    getEmailLog.mockResolvedValue(log);
    renderModal();

    await waitFor(() => expect(screen.getByText('Normal request')).toBeInTheDocument());
    expect(screen.queryByTestId('email-log-rate-limited-banner')).not.toBeInTheDocument();
    expect(screen.queryByTestId('email-log-rate-limited-badge')).not.toBeInTheDocument();
    expect(screen.queryByTestId('email-log-requeue-button')).not.toBeInTheDocument();
  });

  it('renders per-intake watermarks and rate-limit counts', async () => {
    const log = structuredClone(rateLimitedLog);
    log.state.intakes = [
      {
        intake_id: 3,
        folder: 'Support',
        target_type: 'workspace',
        target_id: 1,
        status: 'enabled',
        last_uid: 88,
        uid_validity: 42,
        last_polled_at: '2026-09-23T10:00:00Z',
        rate_limited_count: 2,
      },
    ];
    getEmailLog.mockResolvedValue(log);
    renderModal();

    const row = await screen.findByTestId('email-log-intake-3');
    expect(row).toHaveTextContent('Support');
    expect(row).toHaveTextContent('88');
    expect(row).toHaveTextContent('2');
  });

  const outboxPage = {
    replies: [
      {
        id: 101,
        comment_id: 21,
        item_id: 42,
        to_email: 'customer@example.com',
        to_name: 'Customer One',
        subject: 'Re: Help',
        attempt_count: 2,
        next_attempt_at: '2026-09-23T11:00:00Z',
        last_error: 'smtp relay down',
        created_at: '2026-09-23T10:00:00Z',
      },
      {
        id: 102,
        comment_id: 22,
        item_id: 42,
        to_email: 'other@example.com',
        to_name: 'Other',
        subject: 'Re: Other',
        attempt_count: 0,
        next_attempt_at: '2026-09-23T10:30:00Z',
        created_at: '2026-09-23T10:00:00Z',
      },
    ],
    total: 2,
    page: 1,
    page_size: 50,
  };

  it('lists the customer-reply queue with per-row status and recovery actions', async () => {
    getEmailLog.mockResolvedValue(structuredClone(rateLimitedLog));
    getEmailReplies.mockResolvedValue(structuredClone(outboxPage));
    renderModal();

    await waitFor(() => expect(screen.getByText('Normal request')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('email-log-tab-outbox'));

    await waitFor(() => expect(screen.getByTestId('email-outbox-section')).toBeInTheDocument());
    expect(getEmailReplies).toHaveBeenCalledWith(7, 'pending', 1, 50);

    // Failed row shows its scheduled-retry status and last error.
    await waitFor(() => expect(screen.getByTestId('email-outbox-status-101')).toBeInTheDocument());
    expect(screen.getByTestId('email-outbox-status-101')).toHaveTextContent('2');
    expect(screen.getByText('smtp relay down')).toBeInTheDocument();
    // Pending row never retried shows the pending badge.
    expect(screen.getByTestId('email-outbox-status-102')).toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('email-outbox-retry-101'));
    await waitFor(() => expect(retryEmailReply).toHaveBeenCalledWith(7, 21));
    await waitFor(() => expect(getEmailReplies).toHaveBeenCalledTimes(2));

    await fireEvent.click(screen.getByTestId('email-outbox-discard-102'));
    await waitFor(() => expect(discardEmailReply).toHaveBeenCalledWith(7, 22));
  });

  it('renders one row per recipient even when a comment has several', async () => {
    // The outbox is per-recipient, so two rows can share a comment_id. Keying
    // the table by comment_id crashed Svelte with each_key_duplicate.
    getEmailLog.mockResolvedValue(structuredClone(rateLimitedLog));
    getEmailReplies.mockResolvedValue({
      replies: [
        {
          id: 201,
          comment_id: 30,
          item_id: 42,
          to_email: 'customer@example.com',
          subject: 'Re: Help',
          attempt_count: 0,
          created_at: '2026-09-23T10:00:00Z',
        },
        {
          id: 202,
          comment_id: 30,
          item_id: 42,
          to_email: 'participant@example.com',
          subject: 'Re: Help',
          attempt_count: 0,
          created_at: '2026-09-23T10:00:00Z',
        },
      ],
      total: 2,
      page: 1,
      page_size: 50,
    });
    renderModal();

    await fireEvent.click(screen.getByTestId('email-log-tab-outbox'));

    expect(await screen.findByTestId('email-outbox-status-201')).toBeInTheDocument();
    expect(screen.getByTestId('email-outbox-status-202')).toBeInTheDocument();
  });

  it('renders intake receipts without retry/discard actions', async () => {
    // Intake confirmations live in the same outbox but have no comment_id, and
    // the retry/discard endpoints are comment-keyed. The row must stay visible
    // (status/audit) without offering actions that would call /replies/undefined.
    getEmailLog.mockResolvedValue(structuredClone(rateLimitedLog));
    getEmailReplies.mockResolvedValue({
      replies: [
        {
          id: 301,
          item_id: 42,
          to_email: 'customer@example.com',
          subject: 'We received your request',
          attempt_count: 3,
          last_error: 'smtp relay down',
          created_at: '2026-09-23T10:00:00Z',
        },
        {
          id: 302,
          comment_id: 21,
          item_id: 42,
          to_email: 'customer@example.com',
          subject: 'Re: Help',
          attempt_count: 0,
          created_at: '2026-09-23T10:00:00Z',
        },
      ],
      total: 2,
      page: 1,
      page_size: 50,
    });
    renderModal();

    await fireEvent.click(screen.getByTestId('email-log-tab-outbox'));

    expect(await screen.findByTestId('email-outbox-status-301')).toBeInTheDocument();
    expect(screen.queryByTestId('email-outbox-retry-301')).not.toBeInTheDocument();
    expect(screen.queryByTestId('email-outbox-discard-301')).not.toBeInTheDocument();
    // The comment reply next to it keeps its actions.
    expect(screen.getByTestId('email-outbox-retry-302')).toBeInTheDocument();
  });

  it('shows an empty queue state when nothing is pending', async () => {
    getEmailLog.mockResolvedValue(structuredClone(rateLimitedLog));
    getEmailReplies.mockResolvedValue({ replies: [], total: 0, page: 1, page_size: 50 });
    renderModal();

    await fireEvent.click(screen.getByTestId('email-log-tab-outbox'));
    await waitFor(() => expect(getEmailReplies).toHaveBeenCalled());
    expect(screen.queryByTestId('email-outbox-retry-101')).not.toBeInTheDocument();
  });
});
