import { cleanup, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

vi.mock('../../stores/collectionContext.svelte.js', async () => {
  const { collectionStore } = await import('./subFilterBarStoreMock.svelte.js');
  return { collectionStore };
});
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('./FilterMenu.svelte', async () => ({
  default: (await import('./FilterMenuStub.svelte')).default,
}));

const { collectionStore } = await import('./subFilterBarStoreMock.svelte.js');
const { default: SubFilterBar } = await import('./SubFilterBar.svelte');

beforeEach(() => {
  collectionStore.clearSubFilter();
});

afterEach(cleanup);

it('omits the completion toggle when the board owns completion visibility', () => {
  render(SubFilterBar, { props: { showCompletionToggle: false } });
  expect(screen.queryByTestId('collection-hide-completed')).toBeNull();
});

it('re-syncs the active rows when the store scope resets', async () => {
  collectionStore.setSubFilter('`priority` = "High"', [
    { field: { id: 'priority' }, operator: '=', value: 'High' },
  ]);
  render(SubFilterBar, { props: {} });

  const stub = screen.getByTestId('filter-menu-stub');
  expect(stub.dataset.activeCount).toBe('1');
  expect(stub.dataset.applied).toBe('true');

  // A scope change (MainApp reuses the view instance) clears the store; the
  // bar must stop showing the previous collection's filter.
  collectionStore.clearSubFilter();
  await tick();

  expect(stub.dataset.activeCount).toBe('0');
  expect(stub.dataset.applied).toBe('false');
});
