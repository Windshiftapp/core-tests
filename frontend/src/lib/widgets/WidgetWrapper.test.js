import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

// Resolve the column-count message so the aria text can be asserted without
// loading the real locale bundle in jsdom.
vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) =>
    (key === 'widgets.resizeColumnsValue' ? '{count} of {total} columns' : key).replace(
      /\{(\w+)\}/g,
      (_, name) => (params[name] ?? `{${name}}`)
    ),
}));

import WidgetWrapper from './WidgetWrapper.svelte';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('WidgetWrapper workspace resizing', () => {
  it('uses the active grid scale for pointer resizing', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      bottom: 0,
      height: 0,
      left: 0,
      right: 300,
      top: 0,
      width: 300,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
    const onwidthchange = vi.fn();
    render(WidgetWrapper, {
      props: {
        gridColumns: 3,
        isEditing: true,
        onwidthchange,
        resizeDefaultWidth: 2,
        resizeMaxWidth: 3,
        resizeMinWidth: 1,
        width: 1,
      },
    });

    const handle = screen.getByTestId('widget-resize-handle');
    await fireEvent.mouseDown(handle, { clientX: 0 });
    await fireEvent.mouseMove(window, { clientX: 100 });

    expect(onwidthchange).toHaveBeenLastCalledWith(2);
    expect(handle).toHaveAttribute('aria-valuemax', '3');
  });

  it('renders a visible grip and reports the actual grid column total', () => {
    render(WidgetWrapper, {
      props: {
        gridColumns: 3,
        isEditing: true,
        resizeDefaultWidth: 2,
        resizeMaxWidth: 3,
        resizeMinWidth: 1,
        width: 1,
      },
    });

    const handle = screen.getByTestId('widget-resize-handle');
    expect(handle).toHaveAttribute('aria-valuetext', '1 of 3 columns');
    expect(screen.getByTestId('widget-resize-grip')).toBeInTheDocument();
  });

  it('keeps the resize handle available outside edit mode but out of the tab order', () => {
    render(WidgetWrapper, {
      props: {
        gridColumns: 3,
        isEditing: false,
        resizeDefaultWidth: 2,
        resizeMaxWidth: 3,
        resizeMinWidth: 1,
        width: 1,
      },
    });

    const handle = screen.getByTestId('widget-resize-handle');
    expect(handle).toHaveAttribute('tabindex', '-1');
    expect(handle).toHaveAttribute('aria-valuetext', '1 of 3 columns');
  });

  it('makes the resize handle keyboard-focusable in edit mode', () => {
    render(WidgetWrapper, {
      props: {
        gridColumns: 3,
        isEditing: true,
        resizeDefaultWidth: 2,
        resizeMaxWidth: 3,
        resizeMinWidth: 1,
        width: 1,
      },
    });

    expect(screen.getByTestId('widget-resize-handle')).toHaveAttribute('tabindex', '0');
  });

  it('resets to the active registry default width', async () => {
    const onwidthchange = vi.fn();
    render(WidgetWrapper, {
      props: {
        gridColumns: 3,
        isEditing: true,
        onwidthchange,
        resizeDefaultWidth: 2,
        resizeMaxWidth: 3,
        resizeMinWidth: 1,
        width: 1,
      },
    });

    await fireEvent.dblClick(screen.getByTestId('widget-resize-handle'));

    expect(onwidthchange).toHaveBeenCalledWith(2);
  });
});

describe('WidgetWrapper width menu trigger', () => {
  it('renders a single kebab icon without a built-in chevron', () => {
    render(WidgetWrapper, { props: {} });

    const trigger = screen.getByTestId('widget-width-menu');
    expect(trigger.querySelectorAll('svg')).toHaveLength(1);
    expect(trigger.querySelector('svg')).toHaveClass('lucide-ellipsis');
  });
});
