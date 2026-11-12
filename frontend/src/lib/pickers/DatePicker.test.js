import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

beforeAll(() => {
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import DatePicker from './DatePicker.svelte';

// DatePicker lazy-loads the non-Gregorian field; warm the module cache so the
// Persian test does not race the dynamic import under full-suite load.
beforeAll(async () => {
  await import('./CalendarDateField.svelte');
});

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

describe('DatePicker', () => {
  it('renders a native date input for Gregorian locales', async () => {
    const onchange = vi.fn();
    render(DatePicker, {
      props: { value: '2026-11-07', locale: 'en', dataTestid: 'dp', onchange }
    });

    const input = screen.getByTestId('dp');
    expect(input).toHaveAttribute('type', 'date');
    expect(input).toHaveValue('2026-11-07');

    await fireEvent.change(input, { target: { value: '2026-12-01' } });
    expect(onchange).toHaveBeenCalledWith('2026-12-01', expect.anything());
  });

  it('renders a Jalali trigger and calendar for Persian locales', async () => {
    const onchange = vi.fn();
    render(DatePicker, {
      props: { value: '2026-11-07', locale: 'fa', dataTestid: 'dp', onchange }
    });

    const trigger = await screen.findByTestId('dp');
    expect(trigger.tagName).toBe('BUTTON');
    expect(trigger).toHaveTextContent('۱۶ آبان ۱۴۰۵');

    await fireEvent.click(trigger);

    const day = await screen.findByTestId('dp-calendar-day-2026-11-20');
    await fireEvent.click(day);

    expect(onchange).toHaveBeenCalledTimes(1);
    expect(onchange).toHaveBeenCalledWith('2026-11-20');
  });
});
