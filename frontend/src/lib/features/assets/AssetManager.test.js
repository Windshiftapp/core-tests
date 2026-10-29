import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('../../api/assets.js', () => ({
  assetSets: {
        getAll: vi.fn(),
        getRoles: vi.fn(),
        update: vi.fn(),
        getPortalAccess: vi.fn(),
        setPortalAccess: vi.fn(),
      },
  assetTypes: { getAll: vi.fn(), update: vi.fn(), getFields: vi.fn() },
  assetCategories: { getAll: vi.fn(), create: vi.fn(), update: vi.fn(), move: vi.fn() },
  assetRoles: { getAll: vi.fn() },
}))

vi.mock('../../api/configuration.js', () => ({
  customFields: { getAll: vi.fn() },
}))

vi.mock('../../api/permissions.js', () => ({
  groups: { getAll: vi.fn() },
}))

vi.mock('../../api/users.js', () => ({
  getUsers: vi.fn(),
}));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key, i18n: { locale: 'en' } }));

import { assetCategories, assetRoles, assetSets, assetTypes } from '../../api/assets.js';
import { groups } from '../../api/permissions.js';
import { getUsers } from '../../api/users.js';
import { customFields } from '../../api/configuration.js';
import AssetManager from './AssetManager.svelte';

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const types = (id) => [{ id: id * 10, name: `Type ${id}`, is_active: true }];
const categories = (id) => [{ id: id * 100, name: `Category ${id}`, children: [] }];
const roles = (id) => ({
  user_roles: [{ id, user_name: `User ${id}`, role_name: 'Viewer' }],
  group_roles: [],
  everyone_role: null,
});

const animationDescriptor = Object.getOwnPropertyDescriptor(Element.prototype, 'animate');
const scrollDescriptor = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollIntoView');
beforeEach(() => {
  vi.resetAllMocks();
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    value: () => ({
      finished: Promise.resolve(),
      cancel() {},
      addEventListener() {},
      removeEventListener() {},
    }),
  });
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    value: () => {},
  });
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  );
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: 200,
    bottom: 40,
    width: 200,
    height: 40,
  });
  assetSets.getAll.mockResolvedValue(
    [1, 2].map((id) => ({ id, name: `Set ${id}`, user_permission: 'Administrator' }))
  );
  assetTypes.getAll.mockImplementation((id) => Promise.resolve(types(id)));
  assetCategories.getAll.mockImplementation((id) => Promise.resolve(categories(id)));
  assetCategories.create.mockResolvedValue({});
  assetCategories.update.mockResolvedValue({});
  assetCategories.move.mockResolvedValue({});
  assetSets.getRoles.mockImplementation((id) => Promise.resolve(roles(id)));
  assetSets.update.mockResolvedValue({});
  assetSets.getPortalAccess.mockResolvedValue(null);
  assetSets.setPortalAccess.mockResolvedValue({});
  assetRoles.getAll.mockResolvedValue([]);
  groups.getAll.mockResolvedValue([]);
  getUsers.mockResolvedValue([]);
  assetTypes.update.mockResolvedValue({});
  assetTypes.getFields.mockResolvedValue([]);
  customFields.getAll.mockResolvedValue([]);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  for (const [name, descriptor] of [
    ['animate', animationDescriptor],
    ['scrollIntoView', scrollDescriptor],
  ]) {
    if (descriptor) Object.defineProperty(Element.prototype, name, descriptor);
    else Reflect.deleteProperty(Element.prototype, name);
  }
});

async function selectSet(id) {
  await fireEvent.click(screen.getByTestId('item-picker-trigger'));
  await fireEvent.click(await screen.findByTestId(`asset-manager-set-${id}`));
  await waitFor(() => expect(assetTypes.getAll).toHaveBeenLastCalledWith(id));
}

test('late responses cannot replace another set or change its edit target', async () => {
  const oldTypes = deferred(),
    oldCategories = deferred(),
    oldRoles = deferred();
  assetTypes.getAll.mockImplementation((id) =>
    id === 1 ? oldTypes.promise : Promise.resolve(types(id))
  );
  assetCategories.getAll.mockImplementation((id) =>
    id === 1 ? oldCategories.promise : Promise.resolve(categories(id))
  );
  assetSets.getRoles.mockImplementation((id) =>
    id === 1 ? oldRoles.promise : Promise.resolve(roles(id))
  );
  render(AssetManager);
  await waitFor(() => expect(assetTypes.getAll).toHaveBeenCalledWith(1));
  await selectSet(2);
  await screen.findByText('Type 2');
  oldTypes.resolve(types(1));
  oldCategories.resolve(categories(1));
  oldRoles.resolve(roles(1));
  await Promise.all([oldTypes.promise, oldCategories.promise, oldRoles.promise]);
  await tick();
  expect(screen.queryByText('Type 1')).not.toBeInTheDocument();
  expect(screen.getByText('Type 2')).toBeInTheDocument();
  await fireEvent.click(screen.getByTestId('asset-manager-categories-tab'));
  expect(await screen.findByText('Category 2')).toBeInTheDocument();
  expect(screen.queryByText('Category 1')).not.toBeInTheDocument();
  await fireEvent.click(screen.getByTestId('asset-manager-permissions-tab'));
  expect(await screen.findByText('User 2')).toBeInTheDocument();
  expect(screen.queryByText('User 1')).not.toBeInTheDocument();
  await fireEvent.click(screen.getByTestId('asset-manager-types-tab'));
  await fireEvent.click(screen.getByTestId('asset-manager-edit-type-20'));
  await fireEvent.input(await screen.findByTestId('asset-manager-type-name'), {
    target: { value: 'Edited B' },
  });
  await fireEvent.click(screen.getByTestId('dialog-confirm'));
  await waitFor(() =>
    expect(assetTypes.update).toHaveBeenCalledWith(
      20,
      expect.objectContaining({ name: 'Edited B' })
    )
  );
  expect(assetTypes.update).toHaveBeenCalledTimes(1);
});

test('returning to a set ignores its first response and a failed intervening request', async () => {
  const first = deferred(),
    other = deferred();
  const errorLog = vi.spyOn(console, 'error').mockImplementation(() => {});
  assetTypes.getAll
    .mockReturnValueOnce(first.promise)
    .mockReturnValueOnce(other.promise)
    .mockResolvedValue(types(1));
  render(AssetManager);
  await waitFor(() => expect(assetTypes.getAll).toHaveBeenCalledWith(1));
  await selectSet(2);
  await selectSet(1);
  await screen.findByText('Type 1');
  first.resolve([{ id: 99, name: 'Obsolete type' }]);
  other.reject(new Error('Obsolete request failed'));
  await Promise.allSettled([first.promise, other.promise]);
  await tick();
  expect(screen.getByText('Type 1')).toBeInTheDocument();
  expect(screen.queryByText('Obsolete type')).not.toBeInTheDocument();
  expect(errorLog).not.toHaveBeenCalled();
});

test('a failed load for the selected set leaves no previous set types to edit', async () => {
  const pending = deferred();
  vi.spyOn(console, 'error').mockImplementation(() => {});
  assetTypes.getAll
    .mockReturnValueOnce(Promise.resolve(types(1)))
    .mockReturnValueOnce(pending.promise);
  render(AssetManager);
  await screen.findByText('Type 1');
  await selectSet(2);
  expect(screen.queryByText('Type 1')).not.toBeInTheDocument();
  pending.reject(new Error('Current request failed'));
  await Promise.allSettled([pending.promise]);
  await tick();
  expect(screen.queryByTestId('asset-manager-edit-type-10')).not.toBeInTheDocument();
  expect(screen.getByText('assets.noAssetTypes')).toBeInTheDocument();
  expect(assetTypes.update).not.toHaveBeenCalled();
});

test('a delayed field editor does not open after switching sets', async () => {
  const fields = deferred();
  customFields.getAll.mockReturnValue(fields.promise);
  render(AssetManager);
  await screen.findByText('Type 1');
  await fireEvent.click(screen.getByTestId('asset-manager-type-fields-10'));
  await waitFor(() => expect(assetTypes.getFields).toHaveBeenCalledWith(10));
  await selectSet(2);
  await screen.findByText('Type 2');
  fields.resolve([]);
  await fields.promise;
  await tick();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByText('Type 2')).toBeInTheDocument();
});

test('saving a category blocks set changes and duplicate submission until refresh completes', async () => {
  const save = deferred();
  assetCategories.create.mockReturnValue(save.promise);
  render(AssetManager);
  await screen.findByText('Type 1');
  await fireEvent.click(screen.getByTestId('asset-manager-categories-tab'));
  await fireEvent.click(await screen.findByText('assets.newCategory'));
  await fireEvent.input(screen.getByTestId('asset-manager-category-name'), {
    target: { value: 'New category' },
  });
  await fireEvent.click(screen.getByTestId('dialog-confirm'));

  await waitFor(() => expect(assetCategories.create).toHaveBeenCalledTimes(1));
  expect(screen.getByTestId('item-picker-trigger')).toHaveAttribute('aria-disabled', 'true');
  expect(screen.getByTestId('dialog-confirm')).toBeDisabled();
  expect(screen.getByTestId('dialog-cancel')).toBeDisabled();
  await fireEvent.click(screen.getByTestId('item-picker-trigger'));
  expect(screen.queryByTestId('asset-manager-set-2')).not.toBeInTheDocument();
  await fireEvent.click(screen.getByTestId('dialog-confirm'));
  expect(assetCategories.create).toHaveBeenCalledTimes(1);

  save.resolve({});
  await save.promise;
  await waitFor(() => {
    expect(assetCategories.getAll).toHaveBeenLastCalledWith(1, true);
    expect(screen.getByTestId('item-picker-trigger')).toHaveAttribute('aria-disabled', 'false');
  });
});

test('saving the set form toggles portal access for administrators', async () => {
  assetSets.getPortalAccess.mockResolvedValue(null);
  render(AssetManager);
  await screen.findByText('Type 1');

  await fireEvent.click(screen.getByTestId('asset-manager-set-actions'));
  await fireEvent.click(await screen.findByTestId('asset-manager-edit-set'));
  await waitFor(() => expect(assetSets.getPortalAccess).toHaveBeenCalledWith(1));

  const portalToggle = screen.getByTestId('asset-set-portal-access');
  const checkbox = portalToggle.querySelector('input');
  expect(checkbox).not.toBeChecked();
  await fireEvent.click(checkbox);
  await fireEvent.click(screen.getByTestId('dialog-confirm'));

  await waitFor(() => expect(assetSets.setPortalAccess).toHaveBeenCalledWith(1, true));
  expect(assetSets.update).toHaveBeenCalledTimes(1);
});

test('editing a category patches only name/description and moves on parent change', async () => {
  assetCategories.getAll.mockImplementation(() =>
    Promise.resolve([
      { id: 100, name: 'Category A', description: 'A', parent_id: null, children: [] },
      { id: 101, name: 'Category B', description: 'B', parent_id: null, children: [] },
    ])
  );
  render(AssetManager);
  await screen.findByText('Type 1');
  await fireEvent.click(screen.getByTestId('asset-manager-categories-tab'));
  await fireEvent.click(await screen.findByTestId('asset-manager-edit-category-100'));
  await fireEvent.input(screen.getByTestId('asset-manager-category-name'), {
    target: { value: 'Category A renamed' },
  });

  await fireEvent.click(document.querySelector('#asset-manager-category-parent'));
  await waitFor(() => expect(document.querySelector('[data-option-id="101"]')).toBeInTheDocument());
  await fireEvent.click(document.querySelector('[data-option-id="101"]'));
  await fireEvent.click(screen.getByTestId('dialog-confirm'));

  await waitFor(() =>
    expect(assetCategories.update).toHaveBeenCalledWith(100, {
      name: 'Category A renamed',
      description: 'A',
    })
  );
  expect(assetCategories.move).toHaveBeenCalledWith(100, { parent_id: 101 });
});

test('hides set deletion from delegated set administrators', async () => {
  render(AssetManager);
  await screen.findByText('Type 1');
  await fireEvent.click(screen.getByTestId('asset-manager-set-actions'));
  await screen.findByTestId('asset-manager-edit-set');
  expect(screen.queryByTestId('asset-manager-delete-set')).not.toBeInTheDocument();
});

test('a failed category save unlocks the current set and keeps the form open', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
  assetCategories.create.mockRejectedValue(new Error('save failed'));
  render(AssetManager);
  await screen.findByText('Type 1');
  await fireEvent.click(screen.getByTestId('asset-manager-categories-tab'));
  await fireEvent.click(await screen.findByText('assets.newCategory'));
  await fireEvent.input(screen.getByTestId('asset-manager-category-name'), {
    target: { value: 'New category' },
  });
  await fireEvent.click(screen.getByTestId('dialog-confirm'));

  await waitFor(() => {
    expect(assetCategories.create).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('item-picker-trigger')).toHaveAttribute('aria-disabled', 'false');
  });
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(screen.getByTestId('dialog-confirm')).not.toBeDisabled();
});
