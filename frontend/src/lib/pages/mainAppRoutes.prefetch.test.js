import { waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Fresh module per test: the prefetch dedupe set is module state.
let mainAppRoutes;

/** Points one registry loader at a stub so tests never import the real
 * route component (which would pull its whole subtree into the test). */
function stubLoader(key, implementation) {
  const spy = vi.spyOn(mainAppRoutes.MAIN_APP_COMPONENT_LOADERS, key);
  spy.mockImplementation(implementation);
  return spy;
}

beforeEach(async () => {
  vi.resetModules();
  mainAppRoutes = await import('./mainAppRoutes.js');
});

afterEach(() => {
  vi.restoreAllMocks();
  delete globalThis.requestIdleCallback;
});

describe('prefetchMainAppComponent', () => {
  it('fetches a registered route chunk exactly once across repeated calls', async () => {
    const loader = stubLoader('homepage', vi.fn().mockResolvedValue({ default: {} }));

    mainAppRoutes.prefetchMainAppComponent('homepage');
    mainAppRoutes.prefetchMainAppComponent('homepage');
    mainAppRoutes.prefetchMainAppComponent('homepage');

    await waitFor(() => expect(loader).toHaveBeenCalledTimes(1));
  });

  it('ignores keys that have no loader', () => {
    expect(() => mainAppRoutes.prefetchMainAppComponent('not-a-route')).not.toThrow();
    expect(() => mainAppRoutes.prefetchMainAppComponent(null)).not.toThrow();
  });

  it('allows a retry after a failed prefetch so navigation is not blocked', async () => {
    const loader = stubLoader(
      'homepage',
      vi
        .fn()
        .mockRejectedValueOnce(new Error('chunk unavailable'))
        .mockResolvedValue({ default: {} })
    );

    mainAppRoutes.prefetchMainAppComponent('homepage');
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(1));
    // Let the rejection handler clear the dedupe entry.
    await Promise.resolve();

    mainAppRoutes.prefetchMainAppComponent('homepage');
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(2));
  });
});

describe('prefetchMainAppForView', () => {
  it('maps a view through the route config, including matchViews aliases', async () => {
    // 'hub-inbox' is a matchViews alias of the 'hub' loader key.
    const hubLoader = stubLoader('hub', vi.fn().mockResolvedValue({ default: {} }));

    mainAppRoutes.prefetchMainAppForView('hub-inbox');

    await waitFor(() => expect(hubLoader).toHaveBeenCalledTimes(1));
  });

  it('does nothing for views without a route config', () => {
    const loader = stubLoader('homepage', vi.fn().mockResolvedValue({ default: {} }));

    mainAppRoutes.prefetchMainAppForView('not-a-view');

    expect(loader).not.toHaveBeenCalled();
  });
});

describe('prefetchIdleMainAppComponents', () => {
  it('is a no-op without requestIdleCallback (test environments)', async () => {
    const loader = stubLoader('homepage', vi.fn().mockResolvedValue({ default: {} }));

    mainAppRoutes.prefetchIdleMainAppComponents();
    await Promise.resolve();

    expect(loader).not.toHaveBeenCalled();
  });

  it('prefetches the likely next routes when the browser is idle', async () => {
    const homepageLoader = stubLoader('homepage', vi.fn().mockResolvedValue({ default: {} }));
    const searchLoader = stubLoader('search', vi.fn().mockResolvedValue({ default: {} }));
    globalThis.requestIdleCallback = (callback) => {
      callback();
      return 0;
    };

    mainAppRoutes.prefetchIdleMainAppComponents();

    await waitFor(() => expect(homepageLoader).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(searchLoader).toHaveBeenCalledTimes(1));
  });
});
