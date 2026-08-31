import { describe, expect, it, vi } from 'vitest';
import { LazyComponentLoader } from './lazyComponentLoader.svelte.js';

describe('LazyComponentLoader', () => {
  it('keeps a rejected component in a terminal error state', async () => {
    const error = new TypeError('Failed to fetch dynamically imported module');
    const loader = vi.fn().mockRejectedValue(error);
    const onError = vi.fn();
    const components = new LazyComponentLoader({ create: loader }, { onError });

    await expect(components.load('create')).resolves.toBeNull();

    expect(components.getError('create')).toBe(error);
    expect(components.isLoading('create')).toBe(false);
    expect(onError).toHaveBeenCalledWith('create', error);

    await components.load('create');
    expect(loader).toHaveBeenCalledTimes(1);
  });

  it('loads a failed component again only after an explicit retry', async () => {
    const component = () => null;
    const loader = vi
      .fn()
      .mockRejectedValueOnce(new Error('chunk unavailable'))
      .mockResolvedValueOnce({ default: component });
    const components = new LazyComponentLoader({ create: loader });

    await components.load('create');
    await expect(components.retry('create')).resolves.toBe(component);

    expect(loader).toHaveBeenCalledTimes(2);
    expect(components.getError('create')).toBeNull();
    expect(components.getComponent('create')).toBe(component);
  });

  it('deduplicates concurrent imports for the same component', async () => {
    let resolveImport;
    const loader = vi.fn(
      () =>
        new Promise((resolve) => {
          resolveImport = resolve;
        })
    );
    const components = new LazyComponentLoader({ create: loader });

    const firstLoad = components.load('create');
    await components.load('create');
    resolveImport({ default: () => null });
    await firstLoad;

    expect(loader).toHaveBeenCalledTimes(1);
  });
});
