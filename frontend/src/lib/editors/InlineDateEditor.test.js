import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en-US' },
  t: (key) => key,
}));

import InlineDateEditor from './InlineDateEditor.svelte';

describe('InlineDateEditor', () => {
  test('saves the date-only value required by the item update endpoint', async () => {
    const onsave = vi.fn();
    const { container } = render(InlineDateEditor, {
      props: { value: '2026-08-12T00:00:00Z', onsave },
    });

    await fireEvent.click(screen.getByRole('button'));
    const input = container.querySelector('input[type="date"]');
    expect(input).not.toBeNull();
    expect(input.value).toBe('2026-08-12');

    await fireEvent.input(input, { target: { value: '2030-06-15' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onsave).toHaveBeenCalledWith({ value: '2030-06-15' });
  });
});
