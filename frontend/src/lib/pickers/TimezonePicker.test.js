import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en' },
  t: (key) => key,
}));

vi.mock('../utils/timeUtils.js', () => ({
  listIanaTimezones: () => ['UTC', 'Europe/Berlin'],
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

import TimezonePicker from './TimezonePicker.svelte';

afterEach(() => {
  cleanup();
  // The dropdown is portaled to document.body and can outlive the component.
  document.body.innerHTML = '';
});

describe('TimezonePicker', () => {
  it('offers the known IANA zones as a combobox', async () => {
    render(TimezonePicker, { value: 'UTC' });

    const input = screen.getByPlaceholderText('users.timezone');
    await fireEvent.click(input);

    await waitFor(() => {
      expect(document.querySelector('[data-option-value="Europe/Berlin"]')).toBeInTheDocument();
    });
    expect(document.querySelector('[data-option-value="UTC"]')).toBeInTheDocument();
  });

  it('keeps a stored value visible when the runtime no longer lists it', async () => {
    render(TimezonePicker, { value: 'Local' });

    const input = screen.getByPlaceholderText('users.timezone');
    await fireEvent.click(input);

    await waitFor(() => {
      expect(document.querySelector('[data-option-value="Local"]')).toBeInTheDocument();
    });
  });

  it('reports the selected zone through the bound value', async () => {
    render(TimezonePicker, { value: 'UTC' });

    const input = screen.getByPlaceholderText('users.timezone');
    await fireEvent.click(input);
    const option = await waitFor(() => {
      const el = document.querySelector('[data-option-value="Europe/Berlin"]');
      expect(el).toBeInTheDocument();
      return el;
    });
    await fireEvent.click(option);

    expect(input.value).toContain('Europe/Berlin');
  });
});
