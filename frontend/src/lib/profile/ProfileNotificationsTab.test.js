import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
}));

vi.mock('../api/users.js', () => ({
  userPreferences: { get: mocks.get, update: mocks.update },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import ProfileNotificationsTab from './ProfileNotificationsTab.svelte';

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
  mocks.get.mockReset();
  mocks.update.mockReset();
});

describe('ProfileNotificationsTab due date reminders', () => {
  it('hydrates the saved opt-in and lead time', async () => {
    mocks.get.mockResolvedValue({
      due_date_reminders_enabled: true,
      due_date_reminder_days: 7,
    });

    render(ProfileNotificationsTab);

    await waitFor(() => {
      expect(screen.getByTestId('due-date-reminder-days')).toHaveValue(7);
    });
    expect(screen.getByTestId('due-date-reminders-toggle')).toHaveAttribute('aria-checked', 'true');
  });

  it('persists the enabled flag and lead time on save', async () => {
    mocks.get.mockResolvedValue({
      due_date_reminders_enabled: false,
      due_date_reminder_days: 3,
    });
    mocks.update.mockResolvedValue({
      due_date_reminders_enabled: true,
      due_date_reminder_days: 5,
    });

    render(ProfileNotificationsTab);
    await waitFor(() => {
      expect(screen.getByTestId('due-date-reminders-toggle')).toHaveAttribute(
        'aria-checked',
        'false'
      );
    });

    await fireEvent.click(screen.getByTestId('due-date-reminders-toggle'));
    const days = await screen.findByTestId('due-date-reminder-days');
    await fireEvent.input(days, { target: { value: '5' } });
    await fireEvent.click(screen.getByTestId('due-date-reminders-save'));

    await waitFor(() => {
      expect(mocks.update).toHaveBeenCalledWith({
        due_date_reminders_enabled: true,
        due_date_reminder_days: 5,
      });
    });
  });
});
