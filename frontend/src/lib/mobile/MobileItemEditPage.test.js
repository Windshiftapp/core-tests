import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

const routerMocks = vi.hoisted(() => ({ currentRoute: undefined }));
vi.mock('../router.js', async () => {
  const { writable } = await import('svelte/store');
  routerMocks.currentRoute = writable({
    path: '/m/items/5/edit',
    view: 'mobile-item-edit',
    params: { id: '5' },
    query: {},
  });
  return {
    navigate: vi.fn(),
    setNavigationInterceptor: vi.fn(),
    currentRoute: routerMocks.currentRoute,
  };
});

const apiMocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn() }));
vi.mock('../api/items.js', () => ({
  items: { get: apiMocks.get, update: apiMocks.update },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
  translateError: (err) => err.message,
}));

import { navigate, setNavigationInterceptor } from '../router.js';
import MobileItemEditPage from './MobileItemEditPage.svelte';

const ITEM = {
  id: 5,
  workspace_key: 'WI',
  workspace_item_number: 5,
  title: 'Original',
  description: 'Body',
};

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  apiMocks.get.mockResolvedValue({ ...ITEM });
  apiMocks.update.mockResolvedValue({ ...ITEM, title: 'Edited' });
});

afterEach(cleanup);

test('edits persist as a draft and restore on remount', async () => {
  const { unmount } = render(MobileItemEditPage);
  const title = await screen.findByTestId('item-edit-title');

  await fireEvent.input(title, { target: { value: 'Edited' } });
  expect(sessionStorage.getItem('ws-draft:m-edit:5')).toContain('Edited');
  unmount();

  render(MobileItemEditPage);
  expect(await screen.findByTestId('item-edit-title')).toHaveValue('Edited');
});

test('saving clears the draft and returns to the detail page', async () => {
  render(MobileItemEditPage);
  const title = await screen.findByTestId('item-edit-title');

  await fireEvent.input(title, { target: { value: 'Edited' } });
  await fireEvent.click(screen.getByTestId('editor-save'));

  await waitFor(() =>
    expect(apiMocks.update).toHaveBeenCalledWith(5, {
      title: 'Edited',
      description: 'Body',
    })
  );
  expect(sessionStorage.getItem('ws-draft:m-edit:5')).toBeNull();
  expect(navigate).toHaveBeenCalledWith('/m/items/5', { replace: true });
});

test('registers the navigation interceptor and clears it on unmount', async () => {
  const { unmount } = render(MobileItemEditPage);
  await screen.findByTestId('item-edit-title');

  expect(setNavigationInterceptor).toHaveBeenCalledTimes(1);
  expect(typeof setNavigationInterceptor.mock.calls[0][0]).toBe('function');

  unmount();
  expect(setNavigationInterceptor).toHaveBeenLastCalledWith(null);
});
