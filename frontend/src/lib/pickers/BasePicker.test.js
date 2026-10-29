import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';

beforeAll(() => {
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

// A multi-select value missing from the (lazily loaded) items renders as an
// "unresolved" chip labeled via resolveMissingLabel. The chip must behave
// like any other: removable, and keyed so several unresolved chips never
// collide.
describe('BasePicker — unresolved multi-select chips', () => {
  function renderWithUnresolved(onChange, values = [1, 99]) {
    return render(BasePicker, {
      props: {
        items,
        multiple: true,
        value: values,
        resolveMissingLabel: (v) => (v === 99 ? 'Legacy option' : v === 98 ? 'Older option' : null),
        onChange,
      },
    });
  }

  test('a value missing from items renders as a labeled chip', async () => {
    renderWithUnresolved(vi.fn());

    expect(screen.getByText('Apple')).toBeInTheDocument();
    expect(screen.getByText('Legacy option')).toBeInTheDocument();
  });

  test('removing an unresolved chip removes its raw value', async () => {
    const onChange = vi.fn();
    renderWithUnresolved(onChange);

    const chip = screen.getByText('Legacy option').closest('div');
    await fireEvent.click(chip.querySelector('button.picker-clear'));

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith([1]);
  });

  test('two unresolved chips render side by side and stay individually removable', async () => {
    const onChange = vi.fn();
    renderWithUnresolved(onChange, [98, 99]);

    expect(screen.getByText('Older option')).toBeInTheDocument();
    expect(screen.getByText('Legacy option')).toBeInTheDocument();

    await fireEvent.click(
      screen.getByText('Older option').closest('div').querySelector('button.picker-clear')
    );

    expect(onChange).toHaveBeenCalledWith([99]);
    expect(screen.getByText('Legacy option')).toBeInTheDocument();
    expect(screen.queryByText('Older option')).not.toBeInTheDocument();
  });
});

describe('BasePicker — rendered option cap (WI-1445)', () => {
  function makeItems(count) {
    return Array.from({ length: count }, (_, i) => ({
      id: i + 1,
      name: `Workspace ${String(i + 1).padStart(4, '0')}`,
    }));
  }

  test('mounts only the first page of options and shows the truncation hint', async () => {
    render(BasePicker, { props: { items: makeItems(250) } });

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);

    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });
    expect(screen.getByTestId('picker-more-hint')).toBeInTheDocument();
    // Options past the cap are not mounted.
    expect(document.querySelector('[data-option-value="101"]')).toBeNull();
    expect(document.querySelectorAll('[data-melt-combobox-option]').length).toBe(100);
  });

  test('narrowing the search resets the window and hides the hint', async () => {
    render(BasePicker, { props: { items: makeItems(250) } });

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'Workspace 024' } });

    // Matches 0240..0249 — ten items, well under the cap.
    await waitFor(() => {
      expect(screen.queryByTestId('picker-more-hint')).toBeNull();
    });
    expect(document.querySelector('[data-option-value="1"]')).toBeNull();
    expect(document.querySelector('[data-option-value="241"]')).toBeInTheDocument();
  });

  test('ArrowDown at the cap reveals the next page instead of wrapping', async () => {
    const onSelect = vi.fn();
    render(BasePicker, {
      props: { items: makeItems(150), maxVisibleOptions: 100, onSelect },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });

    // Walk to the end of the first page (99 ArrowDowns from index 0).
    for (let i = 0; i < 99; i++) {
      await fireEvent.keyDown(input, { key: 'ArrowDown' });
    }
    // One more ArrowDown at the last visible option reveals the rest and the
    // highlight moves onto the first newly revealed option (index 100).
    await fireEvent.keyDown(input, { key: 'ArrowDown' });

    await waitFor(() => {
      expect(document.querySelector('[data-option-value="101"]')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('picker-more-hint')).toBeNull();

    // Enter selects the highlighted newly revealed option.
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith(makeItems(150)[100]);
  });
});

describe('BasePicker — grouped options', () => {
  const groupedItems = [
    { id: 1, name: 'Apple', group: 'Fruit' },
    { id: 2, name: 'Banana', group: 'Fruit' },
    { id: 3, name: 'Carrot', group: 'Vegetable' },
  ];

  test('renders a header per group and keeps keyboard selection aligned', async () => {
    const onSelect = vi.fn();
    render(BasePicker, {
      props: { items: groupedItems, groupBy: (item) => item.group, onSelect },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });

    // One header per group, in first-appearance order.
    expect(screen.getByText('Fruit')).toBeInTheDocument();
    expect(screen.getByText('Vegetable')).toBeInTheDocument();

    // The highlight walks the flat option list, not the headers: the second
    // option is Banana even though a header sits between Apple and it.
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith(groupedItems[1]);
  });
});
