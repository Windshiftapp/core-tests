// Reactive stand-in for the collection store so the hierarchy loader can be
// exercised without the full store's fetch/reconcile machinery.
export function createHierarchyStoreMock() {
  let items = $state([]);
  return {
    get items() {
      return items;
    },
    set items(value) {
      items = value;
    },
    hierarchyLevel: null,
    dataGeneration: 0,
    effectiveSubFilterQL: '',
  };
}

export const hierarchyStore = createHierarchyStoreMock();
