import { fireEvent, render } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';
import Input from './Input.svelte';
import NativeSelect from './NativeSelect.svelte';
import Textarea from './Textarea.svelte';

describe('standard form controls', () => {
  test('Input forwards input and blur events from the rendered control', async () => {
    const oninput = vi.fn();
    const onblur = vi.fn();

    render(Input, {
      props: {
        value: 'Initial value',
        oninput,
        onblur,
        dataTestid: 'standard-input',
      },
    });

    const input = document.querySelector('[data-testid="standard-input"]');
    expect(input.value).toBe('Initial value');

    await fireEvent.input(input, { target: { value: 'Changed value' } });
    await fireEvent.blur(input);

    expect(oninput).toHaveBeenCalledTimes(1);
    expect(onblur).toHaveBeenCalledTimes(1);
  });

  test('Textarea renders the requested number of rows, spellcheck setting, and forwards input', async () => {
    const oninput = vi.fn();

    render(Textarea, {
      props: {
        rows: 4,
        spellcheck: false,
        value: 'Script body',
        oninput,
      },
    });

    const textarea = document.querySelector('textarea');
    expect(textarea.rows).toBe(4);
    expect(textarea.getAttribute('spellcheck')).toBe('false');
    expect(textarea.value).toBe('Script body');

    await fireEvent.input(textarea, { target: { value: 'Updated script' } });
    expect(oninput).toHaveBeenCalledTimes(1);
  });

  test('NativeSelect reports the selected option value', async () => {
    const onchange = vi.fn();

    render(NativeSelect, {
      props: {
        value: 'sequential',
        options: [
          { value: 'sequential', label: 'Sequential' },
          { value: 'parallel', label: 'Parallel' },
        ],
        onchange,
      },
    });

    const select = document.querySelector('select');
    expect(select.value).toBe('sequential');

    await fireEvent.change(select, { target: { value: 'parallel' } });
    expect(onchange).toHaveBeenCalledWith('parallel', expect.any(Event));
  });
});
