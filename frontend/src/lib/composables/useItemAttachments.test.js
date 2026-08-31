import { render, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getByItem: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    attachments: {
      getByItem: mocks.getByItem,
    },
  },
}));

vi.mock('../stores', () => ({
  attachmentStatus: {
    enabled: true,
    load: vi.fn(),
  },
}));

import ItemAttachmentsHarness from './ItemAttachmentsHarness.svelte';

afterEach(() => {
  window.dispatchEvent(new Event('pageshow'));
  mocks.getByItem.mockReset();
  vi.restoreAllMocks();
});

async function renderHarness() {
  let manager;
  const view = render(ItemAttachmentsHarness, {
    props: {
      onready: (nextManager) => {
        manager = nextManager;
      },
    },
  });
  await waitFor(() => expect(manager).toBeDefined());
  return { manager, view };
}

describe('useItemAttachments load lifecycle', () => {
  test('ignores a request failure after its component is destroyed', async () => {
    let rejectRequest;
    mocks.getByItem.mockReturnValue(
      new Promise((_, reject) => {
        rejectRequest = reject;
      })
    );
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { manager, view } = await renderHarness();

    const loadPromise = manager.load();
    view.unmount();
    rejectRequest(new TypeError('Failed to fetch'));
    await loadPromise;

    expect(errorSpy).not.toHaveBeenCalled();
  });

  test('ignores a request failure while the document is unloading', async () => {
    let rejectRequest;
    mocks.getByItem.mockReturnValue(
      new Promise((_, reject) => {
        rejectRequest = reject;
      })
    );
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { manager } = await renderHarness();

    const loadPromise = manager.load();
    window.dispatchEvent(new Event('pagehide'));
    rejectRequest(new TypeError('Failed to fetch'));
    await loadPromise;

    expect(errorSpy).not.toHaveBeenCalled();
  });

  test('reports a request failure while its component remains mounted', async () => {
    const requestError = new TypeError('Failed to fetch');
    mocks.getByItem.mockRejectedValue(requestError);
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { manager } = await renderHarness();

    await manager.load();

    expect(errorSpy).toHaveBeenCalledWith('Failed to load attachments:', requestError);
  });
});
