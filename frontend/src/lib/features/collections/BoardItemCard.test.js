import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../sla/slaState.js', () => ({
  getItemSLA: vi.fn(),
  getSLAThresholds: vi.fn(),
  onSLAThresholdsInvalidated: vi.fn(() => () => {}),
}));

import { getItemSLA, getSLAThresholds } from '../sla/slaState.js';
import BoardItemCard from './BoardItemCard.svelte';

function flushEffects() {
  return new Promise((resolve) => queueMicrotask(resolve));
}

beforeEach(() => {
  vi.clearAllMocks();
  getSLAThresholds.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('BoardItemCard metadata footer', () => {
  function renderCard() {
    return render(BoardItemCard, {
      props: {
        item: {
          id: 42,
          workspace_key: 'WIND',
          workspace_item_number: 42,
          title: 'Footer-only metadata',
          item_type_id: 7,
        },
        workspace: { id: 1, key: 'WIND' },
        itemTypes: [{ id: 7, name: 'Story', icon: 'BookOpen', color: '#8b5cf6' }],
      },
    });
  }

  test('renders a one-line card without a footer divider', () => {
    renderCard();

    const footer = screen.getByTestId('board-card-footer-42');

    expect(footer).not.toHaveClass('border-t');
    expect(footer).not.toHaveAttribute('style');
  });

  test('always groups the colored item type icon and sans-serif key at the left', () => {
    renderCard();

    const footer = screen.getByTestId('board-card-footer-42');
    const leftMetadata = footer.firstElementChild;
    const typeIcon = screen.getByTestId('board-card-type-icon-42');
    const itemKey = within(footer).getByText('WIND-42');

    expect(leftMetadata).toContainElement(typeIcon);
    expect(leftMetadata).toContainElement(itemKey);
    expect(typeIcon).toHaveAttribute('title', 'Story');
    expect(itemKey).not.toHaveClass('font-mono');
    expect(screen.queryByTitle('Item type')).not.toBeInTheDocument();
    expect(screen.queryByText('Story')).not.toBeInTheDocument();
  });
});

describe('BoardItemCard item type icon', () => {
  test('renders the server-provided type icon when the workspace catalog omits the type', () => {
    render(BoardItemCard, {
      props: {
        item: {
          id: 88,
          workspace_key: 'WIND',
          workspace_item_number: 88,
          title: 'Excluded type card',
          item_type_id: 1,
          item_type_name: 'Initiative',
          item_type_icon: 'Target',
          item_type_color: '#7c3aed',
        },
        workspace: { id: 1, key: 'WIND' },
        // The workspace catalog intentionally omits types outside the effective
        // configuration set; the card must still render the real icon.
        itemTypes: [{ id: 2, name: 'Epic', icon: 'Zap', color: '#2563eb' }],
      },
    });

    const icon = screen.getByTestId('board-card-type-icon-88');
    expect(icon.querySelector('svg')).toHaveClass('lucide-target');
    expect(icon.querySelector('svg')).not.toHaveClass('lucide-file-text');
    expect(icon).toHaveAttribute('title', 'Initiative');
  });
});

describe('BoardItemCard SLA card field', () => {
  const item = {
    id: 42,
    workspace_id: 7,
    workspace_key: 'WIND',
    workspace_item_number: 42,
    title: 'SLA-gated card',
  };

  test('does not read SLA state or mount the badge when the field is not configured', async () => {
    render(BoardItemCard, { props: { item, cardFields: [] } });
    await flushEffects();

    expect(screen.queryByTestId('board-card-sla-42')).not.toBeInTheDocument();
    expect(getItemSLA).not.toHaveBeenCalled();
    expect(getSLAThresholds).not.toHaveBeenCalled();
  });

  test('mounts the badge and reads SLA state only when the SLA card field is configured', async () => {
    getItemSLA.mockResolvedValue([
      {
        metric_id: 1,
        display_format: 'time',
        ongoing: {
          cycle_no: 1,
          breached: true,
          paused: false,
          within_calendar_hours: true,
          elapsed_ms: 3600001,
          goal_duration_ms: 3600000,
          remaining_ms: -1,
        },
      },
    ]);

    render(BoardItemCard, {
      props: {
        item,
        cardFields: [{ field_type: 'system', field_identifier: 'sla', display_order: 0 }],
      },
    });

    expect(await screen.findByTestId('board-card-sla-42')).toBeInTheDocument();
    expect(getItemSLA).toHaveBeenCalledWith(42, 7);
  });
});

describe('BoardItemCard viewport gating', () => {
  const item = {
    id: 42,
    workspace_id: 7,
    workspace_key: 'WIND',
    workspace_item_number: 42,
    title: 'Viewport-gated card',
  };
  const slaField = [{ field_type: 'system', field_identifier: 'sla', display_order: 0 }];

  test('does not read SLA state for an offscreen card', async () => {
    render(BoardItemCard, { props: { item, cardFields: slaField, visible: false } });
    await flushEffects();

    expect(getItemSLA).not.toHaveBeenCalled();
    expect(getSLAThresholds).not.toHaveBeenCalled();
  });

  test('reports intersection changes once and disconnects on unmount', async () => {
    let callback;
    let disconnected = false;
    let options;
    const observed = [];
    class FakeIntersectionObserver {
      constructor(next, nextOptions) {
        callback = next;
        options = nextOptions;
      }
      observe(element) {
        observed.push(element);
      }
      disconnect() {
        disconnected = true;
      }
    }
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
    const onvisibilitychange = vi.fn();

    const { unmount } = render(BoardItemCard, {
      props: { item, visible: true, onvisibilitychange },
    });
    await flushEffects();

    const element = screen.getByTestId('board-item-42');
    expect(observed).toEqual([element]);
    expect(options).toEqual({ rootMargin: '200px', threshold: 0 });

    callback([{ target: element, isIntersecting: true }]);
    callback([{ target: element, isIntersecting: true }]);
    expect(onvisibilitychange).toHaveBeenCalledTimes(1);
    expect(onvisibilitychange).toHaveBeenCalledWith(42, true);

    callback([{ target: element, isIntersecting: false }]);
    expect(onvisibilitychange).toHaveBeenCalledTimes(2);
    expect(onvisibilitychange).toHaveBeenLastCalledWith(42, false);

    unmount();
    expect(disconnected).toBe(true);
  });
});

describe('BoardItemCard configured fields', () => {
  test('renders global labels returned on the item', () => {
    render(BoardItemCard, {
      props: {
        item: {
          id: 42,
          workspace_key: 'WIND',
          workspace_item_number: 42,
          title: 'Labelled item',
          labels: [{ id: 9, name: 'Customer', color: '#2563eb' }],
        },
        cardFields: [{ field_type: 'system', field_identifier: 'labels', display_order: 0 }],
      },
    });

    expect(screen.getByText('Customer')).toBeInTheDocument();
    expect(screen.getByTitle('Label')).toBeInTheDocument();
  });
});

describe('BoardItemCard selection mode', () => {
  const item = {
    id: 42,
    workspace_key: 'WIND',
    workspace_item_number: 42,
    title: 'Selectable card',
  };

  test('renders no selection control and stays fully opaque when mode is off', () => {
    render(BoardItemCard, { props: { item, onselect: vi.fn() } });

    const card = screen.getByTestId('board-item-42');
    expect(card).toHaveAttribute('data-selection-mode', 'false');
    expect(screen.queryByTestId('board-card-select-42')).not.toBeInTheDocument();
  });

  test('renders the selection control and marks the card as selectable in selection mode', () => {
    render(BoardItemCard, {
      props: { item, onselect: vi.fn(), selectionMode: true },
    });

    const card = screen.getByTestId('board-item-42');
    expect(card).toHaveAttribute('data-selection-mode', 'true');
    expect(card).toHaveAttribute('data-selected', 'false');
    expect(screen.getByTestId('board-card-select-42')).toBeInTheDocument();
  });

  test('reflects the selected state on the card and its control', () => {
    render(BoardItemCard, {
      props: { item, onselect: vi.fn(), selectionMode: true, selected: true },
    });

    expect(screen.getByTestId('board-item-42')).toHaveAttribute('data-selected', 'true');
    expect(screen.getByTestId('board-card-select-42')).toHaveAttribute('aria-pressed', 'true');
  });

  test('toggles selection instead of opening when the card body is clicked in selection mode', async () => {
    const onselect = vi.fn();
    const onopen = vi.fn();
    render(BoardItemCard, {
      props: { item, onselect, onopen, selectionMode: true },
    });

    await fireEvent.click(screen.getByTestId('board-item-42'));

    expect(onselect).toHaveBeenCalledTimes(1);
    expect(onselect).toHaveBeenCalledWith(42, expect.anything());
    expect(onopen).not.toHaveBeenCalled();
  });

  test('opens the card when clicked while selection mode is off', async () => {
    const onselect = vi.fn();
    const onopen = vi.fn();
    render(BoardItemCard, { props: { item, onselect, onopen } });

    await fireEvent.click(screen.getByTestId('board-item-42'));

    expect(onopen).toHaveBeenCalledTimes(1);
    expect(onselect).not.toHaveBeenCalled();
  });

  test('opens without toggling selection when the item key is clicked in selection mode', async () => {
    const onselect = vi.fn();
    const onopen = vi.fn();
    render(BoardItemCard, {
      props: { item, onselect, onopen, selectionMode: true },
    });

    await fireEvent.click(screen.getByText('WIND-42'));

    expect(onopen).toHaveBeenCalledTimes(1);
    expect(onselect).not.toHaveBeenCalled();
  });
});
