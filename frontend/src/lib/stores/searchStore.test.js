import { describe, expect, test } from 'vitest';
import { createWorkItemSearchStore } from './searchStore.svelte.js';

describe('createWorkItemSearchStore workspace queries', () => {
  test('serializes selected workspace IDs as stable workspace keys', () => {
    const store = createWorkItemSearchStore();
    let state;
    const unsubscribe = store.subscribe((value) => {
      state = value;
    });

    store.setWorkspaces([
      { id: 7, key: 'WI', name: 'Windshift' },
      { id: 8, key: 'OPS', name: 'Operations' },
    ]);
    store.setSelectedWorkspaces([7, 8]);

    expect(state.qlQuery).toBe('workspaceKey IN ("WI", "OPS")');
    unsubscribe();
  });
});
