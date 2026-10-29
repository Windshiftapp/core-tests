import { describe, expect, it } from 'vitest';
import { isSelectableTransition, transitionsFromStatus } from './transitionSelection.js';

const directed = (id, from, to) => ({
  id,
  from_status_id: from,
  to_status_id: to,
  from_all_statuses: false,
});
const fromAll = (id, to) => ({
  id,
  from_status_id: null,
  to_status_id: to,
  from_all_statuses: true,
});
const initial = (id, to) => ({
  id,
  from_status_id: null,
  to_status_id: to,
  from_all_statuses: false,
});

describe('isSelectableTransition', () => {
  it('accepts directed and from-all transitions', () => {
    expect(isSelectableTransition(directed(1, 1, 2))).toBe(true);
    expect(isSelectableTransition(fromAll(2, 3))).toBe(true);
  });

  it('rejects item-creating initial transitions', () => {
    expect(isSelectableTransition(initial(3, 1))).toBe(false);
  });
});

describe('transitionsFromStatus', () => {
  it('returns directed rows before from-all rows and keeps initial rows out', () => {
    const transitions = [
      initial(1, 1),
      directed(2, 10, 20),
      fromAll(3, 30),
      directed(4, 11, 21),
    ];

    expect(transitionsFromStatus(transitions, 10).map((tr) => tr.id)).toEqual([2, 3]);
  });

  it('drops a from-all row shadowed by a directed row to the same target', () => {
    const transitions = [directed(1, 10, 20), fromAll(2, 20), fromAll(3, 30)];

    expect(transitionsFromStatus(transitions, 10).map((tr) => tr.id)).toEqual([1, 3]);
  });

  it('returns nothing without a status', () => {
    expect(transitionsFromStatus([directed(1, 10, 20)], null)).toEqual([]);
  });
});
