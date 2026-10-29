import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, expect, it, vi } from 'vitest';

vi.mock('../../stores/collectionContext.svelte.js', () => ({
  collectionStore: { subFilterRows: [], subFilterQL: '', showCompleted: false },
}));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('./FilterMenu.svelte', async () => ({
  default: (await import('./FilterMenuStub.svelte')).default,
}));

const { default: SubFilterBar } = await import('./SubFilterBar.svelte');
afterEach(cleanup);

it('omits the completion toggle when the board owns completion visibility', () => {
  render(SubFilterBar, { props: { showCompletionToggle: false } });
  expect(screen.queryByTestId('collection-hide-completed')).toBeNull();
});
