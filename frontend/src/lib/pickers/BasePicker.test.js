import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';

// jsdom does not implement the Web Animations API. Svelte 5 transitions
// call element.animate during outro. Stub it with a Promise-shaped result
// so transitions resolve immediately and don't crash the test runner.
beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      play: () => {},
      pause: () => {},
    });
  }
  // jsdom also lacks scrollIntoView, which the picker uses to keep the
  // highlighted row visible.
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
  // Melt's floating menu positioning observes the reference element.
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import BasePicker from './BasePicker.svelte';

afterEach(() => {
  // The dropdown menu is portaled to document.body and outros via a fly
  // transition — stragglers can outlive the component between renders.
  // Wipe the body so each test starts clean.
  document.body.innerHTML = '';
});

const items = [
  { id: 1, name: 'Apple' },
  { id: 2, name: 'Apricot' },
  { id: 3, name: 'Banana' },
];

async function openAndType(query) {
  const input = screen.getByRole('combobox');
  await fireEvent.click(input);
  await fireEvent.input(input, { target: { value: query } });
  return input;
}

describe('BasePicker — Enter selects the highlighted option vs create (WI-343)', () => {
  test('single: Enter on a partial query selects the highlighted match, not create', async () => {
    const onSelect = vi.fn();
    const onCreate = vi.fn();
    render(BasePicker, {
      props: { items, allowCreate: true, onCreate, onSelect },
    });

    const input = await openAndType('Ap');
    // Wait for the filtered dropdown to render (Apple highlighted first).
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });

    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onCreate).not.toHaveBeenCalled();
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(items[0]);
  });

  test('single: arrow-key highlight + Enter selects that option, not create', async () => {
    const onSelect = vi.fn();
    const onCreate = vi.fn();
    render(BasePicker, {
      props: { items, allowCreate: true, onCreate, onSelect },
    });

    const input = await openAndType('Ap');
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="2"]')).toBeInTheDocument();
    });

    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onCreate).not.toHaveBeenCalled();
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(items[1]);
  });

  test('single: Enter with zero matches calls onCreate with the query', async () => {
    const onSelect = vi.fn();
    const onCreate = vi.fn();
    render(BasePicker, {
      props: { items, allowCreate: true, onCreate, onSelect },
    });

    // A non-matching query leaves zero selectable options (the outgoing
    // menu DOM may linger mid-transition, but the reactive option list —
    // which the keyboard handler consults — is empty).
    const input = await openAndType('Cherry');
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    expect(onCreate).toHaveBeenCalledWith('Cherry');
    expect(onSelect).not.toHaveBeenCalled();
  });

  test('single: Enter with zero matches but no create support is a no-op', async () => {
    const onSelect = vi.fn();
    render(BasePicker, {
      props: { items, onSelect },
    });

    const input = await openAndType('Cherry');
    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onSelect).not.toHaveBeenCalled();
  });

  test('multiple: Enter on a partial query toggles the highlighted match, not create', async () => {
    const onChange = vi.fn();
    const onCreate = vi.fn();
    render(BasePicker, {
      props: { items, multiple: true, value: [], allowCreate: true, onCreate, onChange },
    });

    const input = await openAndType('Ap');
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });

    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onCreate).not.toHaveBeenCalled();
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith([1]);
  });

  test('multiple: Enter with zero matches calls onCreate with the query', async () => {
    const onChange = vi.fn();
    const onCreate = vi.fn();
    render(BasePicker, {
      props: { items, multiple: true, value: [], allowCreate: true, onCreate, onChange },
    });

    const input = await openAndType('Cherry');
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    expect(onCreate).toHaveBeenCalledWith('Cherry');
    expect(onChange).not.toHaveBeenCalled();
  });

  test('multiple: zero matches exposes a clickable create action', async () => {
    const onCreate = vi.fn();
    render(BasePicker, {
      props: { items, multiple: true, value: [], allowCreate: true, onCreate },
    });

    await openAndType('Cherry');

    const createAction = await screen.findByRole('button', { name: /pickers\.createItem/ });
    await fireEvent.click(createAction);

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    expect(onCreate).toHaveBeenCalledWith('Cherry');
  });

  test('create still refuses an exact label match', async () => {
    const onSelect = vi.fn();
    const onCreate = vi.fn();
    render(BasePicker, {
      props: { items, allowCreate: true, onCreate, onSelect },
    });

    const input = await openAndType('Apple');
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });

    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onCreate).not.toHaveBeenCalled();
    expect(onSelect).toHaveBeenCalledWith(items[0]);
  });
});

describe('BasePicker — option highlight styling', () => {
  test('mouse hover uses the subtle raised-surface hover token', async () => {
    render(BasePicker, { props: { items } });

    await fireEvent.click(screen.getByRole('combobox'));
    const hoveredOption = await waitFor(() => {
      const option = document.querySelector('[data-option-value="2"]');
      expect(option).toBeInTheDocument();
      return option;
    });

    await fireEvent.mouseEnter(hoveredOption);

    await waitFor(() => {
      expect(hoveredOption).toHaveStyle({
        backgroundColor: 'var(--ds-surface-raised-hovered)',
      });
    });
  });
});

// Regression for WI-428: a pre-selected single-select whose items have a
// `label` but no `name` field (e.g. milestone status, {value,label} against
// the default searchFields=['name']) opened empty because the selected item's
// display label — echoed into the combobox input — was treated as a live
// search filter before the user typed anything.
describe('BasePicker — opening a pre-selected single-select does not filter (WI-428)', () => {
  const statusItems = [
    { value: 'planning', label: 'Planning' },
    { value: 'in-progress', label: 'In Progress' },
    { value: 'completed', label: 'Completed' },
  ];

  test('all options render when opening a pre-selected picker without typing', async () => {
    render(BasePicker, {
      props: {
        items: statusItems,
        value: 'planning',
        getValue: (item) => item.value,
        getLabel: (item) => item.label,
      },
    });

    const input = screen.getByRole('combobox');
    // Open the dropdown WITHOUT typing — this is the bug's trigger.
    await fireEvent.click(input);

    await waitFor(() => {
      expect(document.querySelector('[data-option-value="planning"]')).toBeInTheDocument();
      expect(document.querySelector('[data-option-value="in-progress"]')).toBeInTheDocument();
      expect(document.querySelector('[data-option-value="completed"]')).toBeInTheDocument();
    });
  });

  test('typing still filters the list (display text is not sticky)', async () => {
    render(BasePicker, {
      props: {
        items: statusItems,
        value: 'planning',
        getValue: (item) => item.value,
        getLabel: (item) => item.label,
      },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    // Once the user types, the live filter must kick back in.
    await fireEvent.input(input, { target: { value: 'Completed' } });

    await waitFor(() => {
      expect(document.querySelector('[data-option-value="completed"]')).toBeInTheDocument();
      expect(document.querySelector('[data-option-value="planning"]')).not.toBeInTheDocument();
    });
  });
});

describe('BasePicker — popover trigger accessibility', () => {
  test('custom triggers expose one keyboard-operable combobox', async () => {
    const children = createRawSnippet(() => ({
      render: () => '<div data-testid="custom-picker-trigger">Status</div>',
    }));
    render(BasePicker, { props: { items, children } });

    const trigger = screen.getByRole('combobox');
    expect(trigger).toHaveAttribute('tabindex', '0');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger).toHaveAttribute('aria-controls');

    await fireEvent.keyDown(trigger, { key: 'Enter' });

    const dropdown = await screen.findByTestId('picker-dropdown');
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(dropdown.id).toBe(trigger.getAttribute('aria-controls'));
  });
});
