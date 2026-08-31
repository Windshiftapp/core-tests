import { fireEvent, render, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import PullToRefreshHarness from './PullToRefreshHarness.svelte';

/**
 * usePullToRefresh uses Svelte 5 runes ($state + $effect), so it has to run
 * inside a component context. PullToRefreshHarness wires a real scroll element
 * to the composable and reflects its reactive state into data-* attributes,
 * which is what we assert on here.
 */

function mountHarness(props = {}) {
  const onRefresh = vi.fn(() => Promise.resolve());
  const result = render(PullToRefreshHarness, {
    props: { onRefresh, threshold: 64, maxPull: 400, resistance: 1, ...props },
  });
  const scroll = result.getByTestId('ptr-scroll');
  return { ...result, scroll, onRefresh };
}

function attr(el, name) {
  return el.getAttribute(name);
}

function pull(scroll, { distance, startTop = 0 } = {}) {
  scroll.scrollTop = startTop;
  fireEvent.touchStart(scroll, { touches: [{ clientY: 100 }] });
  fireEvent.touchMove(scroll, { touches: [{ clientY: 100 + distance }] });
  fireEvent.touchEnd(scroll);
}

afterEach(() => {
  document.body.innerHTML = '';
});

describe('usePullToRefresh — threshold crossing', () => {
  it('fires onRefresh when a downward pull crosses the threshold on release', async () => {
    const { scroll, onRefresh } = mountHarness({ threshold: 64, resistance: 1 });

    // distance 80 > threshold 64, resistance 1 ⇒ pullDistance 80 ≥ 64.
    pull(scroll, { distance: 80 });

    await Promise.resolve();
    await Promise.resolve();
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('does NOT fire when the pull is below the threshold', () => {
    const { scroll, onRefresh } = mountHarness({ threshold: 64, resistance: 1 });

    pull(scroll, { distance: 40 });
    expect(onRefresh).not.toHaveBeenCalled();
  });
});

describe('usePullToRefresh — scroll guard', () => {
  it('ignores pulls when the container is not scrolled to the top', () => {
    const { scroll, onRefresh } = mountHarness({ threshold: 64, resistance: 1 });

    pull(scroll, { distance: 200, startTop: 50 });
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it('ignores upward drags (negative delta)', () => {
    const { scroll, onRefresh } = mountHarness({ threshold: 64, resistance: 1 });

    fireEvent.touchStart(scroll, { touches: [{ clientY: 100 }] });
    fireEvent.touchMove(scroll, { touches: [{ clientY: 60 }] }); // up
    fireEvent.touchEnd(scroll);
    expect(onRefresh).not.toHaveBeenCalled();
  });
});

describe('usePullToRefresh — re-entrancy', () => {
  it('blocks a second pull while a refresh is in flight', async () => {
    let resolveRefresh;
    const onRefresh = vi.fn(
      () =>
        new Promise((resolve) => {
          resolveRefresh = resolve;
        })
    );
    const { getByTestId } = render(PullToRefreshHarness, {
      props: { onRefresh, threshold: 64, resistance: 1 },
    });
    const scroll = getByTestId('ptr-scroll');

    pull(scroll, { distance: 80 });
    expect(onRefresh).toHaveBeenCalledTimes(1);
    await Promise.resolve();
    expect(attr(scroll, 'data-refreshing')).toBe('true');

    // Second pull while refreshing — must be ignored.
    pull(scroll, { distance: 80 });
    expect(onRefresh).toHaveBeenCalledTimes(1);

    resolveRefresh();
    // The refresh promise resolves, then Svelte flushes refreshing=false to the
    // DOM — wait for that propagation rather than guessing a microtask count.
    await waitFor(() => expect(attr(scroll, 'data-refreshing')).toBe('false'));
  });
});

describe('usePullToRefresh — reactive state', () => {
  it('reports pulling=true during an active drag and resets after release', async () => {
    const { scroll, onRefresh } = mountHarness({ threshold: 64, resistance: 1 });

    fireEvent.touchStart(scroll, { touches: [{ clientY: 100 }] });
    fireEvent.touchMove(scroll, { touches: [{ clientY: 140 }] });
    expect(attr(scroll, 'data-pulling')).toBe('true');
    expect(attr(scroll, 'data-pull-distance')).toBe('40');

    // Below threshold ⇒ snap back, no fire.
    fireEvent.touchEnd(scroll);
    await Promise.resolve();
    expect(attr(scroll, 'data-pulling')).toBe('false');
    expect(attr(scroll, 'data-pull-distance')).toBe('0');
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it('clamps the pull distance to maxPull', async () => {
    const { scroll } = mountHarness({ threshold: 64, maxPull: 50, resistance: 1 });

    fireEvent.touchStart(scroll, { touches: [{ clientY: 100 }] });
    fireEvent.touchMove(scroll, { touches: [{ clientY: 300 }] }); // +200 → clamped to 50
    expect(attr(scroll, 'data-pull-distance')).toBe('50');
    fireEvent.touchEnd(scroll);
    await Promise.resolve();
  });

  it('applies resistance so a large drag maps to a dampened pull', async () => {
    const { scroll } = mountHarness({ threshold: 64, maxPull: 400, resistance: 2 });

    fireEvent.touchStart(scroll, { touches: [{ clientY: 100 }] });
    fireEvent.touchMove(scroll, { touches: [{ clientY: 300 }] }); // +200 → /2 = 100
    expect(attr(scroll, 'data-pull-distance')).toBe('100');
    fireEvent.touchEnd(scroll);
    await Promise.resolve();
  });
});
