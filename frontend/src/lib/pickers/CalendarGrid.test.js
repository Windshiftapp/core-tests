import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en', isRTL: false },
  t: (key) => key
}));

beforeAll(() => {
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import CalendarGrid from './CalendarGrid.svelte';

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

describe('CalendarGrid', () => {
  it('renders the Persian calendar for the fa locale', () => {
    render(CalendarGrid, {
      props: {
        calendarId: 'persian',
        locale: 'fa',
        value: '2026-11-07',
        dataTestid: 'cal'
      }
    });

    expect(screen.getByText('۱۴۰۵ آبان')).toBeInTheDocument();
    const selected = screen.getByTestId('cal-day-2026-11-07');
    expect(selected).toHaveAttribute('data-selected');
    expect(selected).toHaveTextContent('۱۶');
  });

  it('renders the Gregorian calendar for en-US', () => {
    render(CalendarGrid, {
      props: {
        calendarId: 'gregory',
        locale: 'en-US',
        value: '2026-11-07',
        dataTestid: 'cal'
      }
    });

    expect(screen.getByText('November 2026')).toBeInTheDocument();
    const selected = screen.getByTestId('cal-day-2026-11-07');
    expect(selected).toHaveAttribute('data-selected');
    expect(selected).toHaveTextContent('7');
  });

  it('starts the week on Saturday for fa', () => {
    render(CalendarGrid, {
      props: { calendarId: 'persian', locale: 'fa', value: '2026-11-07', dataTestid: 'cal' }
    });

    const headers = document.querySelectorAll('[data-testid="cal"] thead th');
    expect(headers).toHaveLength(7);
    expect(headers[0]).toHaveTextContent('شنبه');
  });

  it('starts the week on Sunday for en-US', () => {
    render(CalendarGrid, {
      props: { calendarId: 'gregory', locale: 'en-US', value: '2026-11-07', dataTestid: 'cal' }
    });

    const headers = document.querySelectorAll('[data-testid="cal"] thead th');
    expect(headers).toHaveLength(7);
    expect(headers[0]).toHaveTextContent('Sun');
  });

  it('emits the Gregorian date when a Persian day is selected', async () => {
    const onselect = vi.fn();
    render(CalendarGrid, {
      props: {
        calendarId: 'persian',
        locale: 'fa',
        value: '2026-11-07',
        onselect,
        dataTestid: 'cal'
      }
    });

    await fireEvent.click(screen.getByTestId('cal-day-2026-11-20'));

    expect(onselect).toHaveBeenCalledTimes(1);
    expect(onselect).toHaveBeenCalledWith('2026-11-20');
  });
});
