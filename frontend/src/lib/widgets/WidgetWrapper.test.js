import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../layout/DropdownMenu.svelte', () => ({
  default: function MockDropdownMenu() {},
}));

vi.mock('@lucide/svelte', () => ({
  Check: function MockCheck() {},
  ChevronDown: function MockChevronDown() {},
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
