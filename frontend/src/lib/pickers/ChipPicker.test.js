import { createEvent, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';

beforeAll(() => {
  // jsdom lacks scrollIntoView, which the picker uses to keep the highlighted
  // row visible.
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
  // Melt's floating popover observes the reference element.
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import ChipPicker from './ChipPicker.svelte';

afterEach(() => {
  // The popover is portalled to document.body; wipe it so each test starts
  // clean.
  document.body.innerHTML = '';
});

const items = [
  { id: 1, name: 'Apple' },
  { id: 2, name: 'Banana' },
];

// Regression: a focused chip picker consumed Escape (preventDefault +
// stopPropagation) even when its popover was closed, trapping the user inside
// any dialog that contains one (for example the create modal).
describe('ChipPicker — Escape ownership', () => {
  test('lets Escape bubble while the popover is closed', async () => {
    render(ChipPicker, { props: { items, value: 1, testId: 'chip' } });
    const trigger = screen.getByTestId('chip');

    const bubble = vi.fn();
    document.addEventListener('keydown', bubble);
    try {
      const event = createEvent.keyDown(trigger, { key: 'Escape' });
      fireEvent(trigger, event);

      expect(event.defaultPrevented).toBe(false);
      expect(bubble).toHaveBeenCalledTimes(1);
      expect(screen.queryByTestId('chip-dropdown')).toBeNull();
    } finally {
      document.removeEventListener('keydown', bubble);
    }
  });

  test('consumes Escape and closes the popover while open', async () => {
    render(ChipPicker, { props: { items, testId: 'chip' } });
    const trigger = screen.getByTestId('chip');
    await fireEvent.click(trigger);
    await screen.findByTestId('chip-listbox');

    const bubble = vi.fn();
    document.addEventListener('keydown', bubble);
    try {
      const event = createEvent.keyDown(trigger, { key: 'Escape' });
      fireEvent(trigger, event);

      expect(event.defaultPrevented).toBe(true);
      expect(bubble).not.toHaveBeenCalled();
      await waitFor(() => expect(screen.queryByTestId('chip-dropdown')).toBeNull());
    } finally {
      document.removeEventListener('keydown', bubble);
    }
  });
});
