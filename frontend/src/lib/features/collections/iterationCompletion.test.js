import { describe, expect, it } from 'vitest';
import { getIncompleteIterationItems } from './iterationCompletion.js';

describe('getIncompleteIterationItems', () => {
  const statuses = [
    { id: 1, name: 'Open', category_id: 10 },
    { id: 2, name: 'Released', category_id: 20 },
    { id: 3, name: 'Done', category_id: 30 },
  ];
  const categories = [
    { id: 10, name: 'In progress', is_completed: false },
    { id: 20, name: 'Closed', is_completed: true },
    { id: 30, name: 'Done', is_completed: false },
  ];

  it('uses the category completion flag instead of its name', () => {
    const items = [
      { id: 1, status_name: 'Open' },
      { id: 2, status_name: 'Released' },
      { id: 3, status_name: 'Done' },
    ];

    expect(getIncompleteIterationItems(items, statuses, categories).map((item) => item.id)).toEqual(
      [1, 3]
    );
  });

  it('treats an unknown status as incomplete', () => {
    const items = [{ id: 4, status_name: 'Missing status' }];

    expect(getIncompleteIterationItems(items, statuses, categories)).toEqual(items);
  });
});
