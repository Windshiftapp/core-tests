// Reactive stand-in for the class-based collectionStore so SubFilterBar tests
// can reset subFilterRows the way a scope change does.
export function createSubFilterStoreMock() {
  let subFilterRows = $state([]);
  let subFilterQL = $state('');
  let showCompleted = $state(false);

  return {
    get subFilterRows() {
      return subFilterRows;
    },
    set subFilterRows(value) {
      subFilterRows = value;
    },
    get subFilterQL() {
      return subFilterQL;
    },
    set subFilterQL(value) {
      subFilterQL = value;
    },
    get showCompleted() {
      return showCompleted;
    },
    set showCompleted(value) {
      showCompleted = value;
    },
    setSubFilter(ql, rows = []) {
      subFilterQL = ql;
      subFilterRows = rows;
    },
    clearSubFilter() {
      subFilterQL = '';
      subFilterRows = [];
    },
    setShowCompleted(value) {
      showCompleted = value;
    },
  };
}

export const collectionStore = createSubFilterStoreMock();
