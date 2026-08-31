import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: { assets: { getAll: vi.fn() } },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params) => (params?.query ? `${key}:${params.query}` : key),
}));

beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
    });
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

const { default: AssetPicker } = await import('./AssetPicker.svelte');

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

describe('AssetPicker lazy collection options (WI-630)', () => {
  test('does not load before opening and discards an obsolete search result', async () => {
    const initial = deferred();
    const olderSearch = deferred();
    const latestSearch = deferred();
    const optionLoader = vi
      .fn()
      .mockReturnValueOnce(initial.promise)
      .mockReturnValueOnce(olderSearch.promise)
      .mockReturnValueOnce(latestSearch.promise);

    render(AssetPicker, { props: { assetSetId: 7, optionLoader } });
    expect(optionLoader).not.toHaveBeenCalled();

    const input = screen.getByRole('combobox');
    await fireEvent.click(input);
    await waitFor(() => expect(optionLoader).toHaveBeenCalledWith(''));
    initial.resolve({ assets: [], total: 0 });

    await fireEvent.input(input, { target: { value: 'old' } });
    await waitFor(() => expect(optionLoader).toHaveBeenCalledWith('old'), { timeout: 1000 });

    await fireEvent.input(input, { target: { value: 'latest' } });
    await waitFor(() => expect(optionLoader).toHaveBeenCalledWith('latest'), { timeout: 1000 });

    latestSearch.resolve({ assets: [{ id: 2, title: 'Latest asset' }], total: 1 });
    await waitFor(() => expect(screen.getByText('Latest asset')).toBeInTheDocument());

    olderSearch.resolve({ assets: [{ id: 1, title: 'Obsolete asset' }], total: 1 });
    await Promise.resolve();

    expect(screen.queryByText('Obsolete asset')).not.toBeInTheDocument();
    expect(screen.getByText('Latest asset')).toBeInTheDocument();
  });

  test('ignores a pending result after its row unmounts', async () => {
    const pending = deferred();
    const optionLoader = vi.fn(() => pending.promise);
    const view = render(AssetPicker, { props: { assetSetId: 7, optionLoader } });

    await fireEvent.click(screen.getByRole('combobox'));
    await waitFor(() => expect(optionLoader).toHaveBeenCalledTimes(1));
    view.unmount();

    pending.resolve({ assets: [{ id: 3, title: 'Unmounted asset' }], total: 1 });
    await Promise.resolve();

    expect(screen.queryByText('Unmounted asset')).not.toBeInTheDocument();
  });
});
