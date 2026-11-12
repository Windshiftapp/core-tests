// Reactive stand-in for the class-based collectionStore so tests can replace
// the items array the way a completed-toggle reload does.
import { vi } from 'vitest';

export function createCollectionStoreMock() {
  let items = $state([]);
  let loading = $state(false);
  let collectionName = $state('Default');
  let collectionTotal = $state(null);
  let itemsPagination = $state(null);
  let itemsHasMore = $state(false);
  let itemsLoadingMore = $state(false);
  let itemsRemainingCount = $state(0);
  let hierarchyLevel = $state(null);
  let levelCounts = $state([]);
  let levelCountsLoading = $state(false);
  const loadMoreItems = vi.fn();
  const ensureLevelCounts = vi.fn().mockResolvedValue([]);
  const setHierarchyLevel = vi.fn((level) => {
    hierarchyLevel = level;
  });

  return {
    loadMoreItems,
    ensureLevelCounts,
    setHierarchyLevel,
    effectiveSubFilterQL: '',
    dataGeneration: 0,
    get hierarchyLevel() {
      return hierarchyLevel;
    },
    set hierarchyLevel(value) {
      hierarchyLevel = value;
    },
    get levelCounts() {
      return levelCounts;
    },
    set levelCounts(value) {
      levelCounts = value;
    },
    get levelCountsLoading() {
      return levelCountsLoading;
    },
    set levelCountsLoading(value) {
      levelCountsLoading = value;
    },
    get itemsHasMore() {
      return itemsHasMore;
    },
    set itemsHasMore(value) {
      itemsHasMore = value;
    },
    get itemsLoadingMore() {
      return itemsLoadingMore;
    },
    set itemsLoadingMore(value) {
      itemsLoadingMore = value;
    },
    get itemsRemainingCount() {
      return itemsRemainingCount;
    },
    set itemsRemainingCount(value) {
      itemsRemainingCount = value;
    },
    get items() {
      return items;
    },
    set items(value) {
      items = value;
    },
    get loading() {
      return loading;
    },
    set loading(value) {
      loading = value;
    },
    get collectionName() {
      return collectionName;
    },
    set collectionName(value) {
      collectionName = value;
    },
    get collectionTotal() {
      return collectionTotal;
    },
    set collectionTotal(value) {
      collectionTotal = value;
    },
    get itemsPagination() {
      return itemsPagination;
    },
    set itemsPagination(value) {
      itemsPagination = value;
    },
  };
}

export const collectionTreeStore = createCollectionStoreMock();
