import { cleanup, fireEvent, render, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/diagnostics.js', () => ({
  getEmailIntakeDiagnostics: vi.fn(),
}));

import { getEmailIntakeDiagnostics } from '../../api/diagnostics.js';
import EmailIntakeSection from './EmailIntakeSection.svelte';

const message = (overrides = {}) => ({
  id: 1,
  channel_id: 2,
  channel_name: 'Support mailbox',
  from_email: 'postmaster@outlook.com',
  from_name: 'Mail Delivery System',
  subject: 'Undeliverable: notifications',
  category: 'delivery_failure',
  processed_at: '2026-10-16T07:10:20Z',
  ...overrides,
});

describe('EmailIntakeSection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => cleanup());

  test('renders skipped system mail with category counts and mailbox', async () => {
    getEmailIntakeDiagnostics.mockResolvedValue({
      counts: [
        { category: 'delivery_failure', count: 2 },
        { category: 'auto_reply', count: 1 },
      ],
      messages: [
        message(),
        message({ id: 2, subject: 'Delivery Status Notification' }),
        message({
          id: 3,
          category: 'auto_reply',
          from_email: 'customer@example.com',
          from_name: 'Customer',
          subject: 'Automatic reply: Out of office',
        }),
      ],
    });

    const view = render(EmailIntakeSection);
    await waitFor(() => expect(getEmailIntakeDiagnostics).toHaveBeenCalledTimes(1));
    expect(getEmailIntakeDiagnostics).toHaveBeenCalledWith({ since: '24h', limit: 100 });

    expect(view.getByTestId('email-intake-counts')).toHaveTextContent('Delivery failure: 2');
    expect(view.getByTestId('email-intake-counts')).toHaveTextContent('Auto-reply: 1');
    expect(view.getByText('3 skipped messages')).toBeInTheDocument();

    const firstRow = view.getByTestId('email-intake-row-1');
    expect(firstRow).toHaveTextContent('Delivery failure');
    expect(firstRow).toHaveTextContent('postmaster@outlook.com');
    expect(firstRow).toHaveTextContent('Undeliverable: notifications');
    expect(firstRow).toHaveTextContent('Support mailbox');
    expect(view.getByTestId('email-intake-delivery-hint')).toBeInTheDocument();
  });

  test('renders an empty state when nothing was skipped', async () => {
    getEmailIntakeDiagnostics.mockResolvedValue({ counts: [], messages: [] });
    const view = render(EmailIntakeSection);
    await waitFor(() => expect(getEmailIntakeDiagnostics).toHaveBeenCalledTimes(1));
    expect(view.getByText('0 skipped messages')).toBeInTheDocument();
    expect(
      view.getByText('No system-generated mail was skipped in this window.')
    ).toBeInTheDocument();
  });

  test('reloads with the selected window', async () => {
    getEmailIntakeDiagnostics.mockResolvedValue({ counts: [], messages: [] });
    const view = render(EmailIntakeSection);
    await waitFor(() => expect(getEmailIntakeDiagnostics).toHaveBeenCalledTimes(1));

    await fireEvent.change(view.getByTestId('email-intake-window'), {
      target: { value: '168h' },
    });
    await waitFor(() =>
      expect(getEmailIntakeDiagnostics).toHaveBeenLastCalledWith({ since: '168h', limit: 100 })
    );
  });

  test('renders request failures', async () => {
    getEmailIntakeDiagnostics.mockRejectedValue(new Error('diagnostics unavailable'));
    const view = render(EmailIntakeSection);
    await waitFor(() => expect(view.getByText('diagnostics unavailable')).toBeInTheDocument());
  });
});
