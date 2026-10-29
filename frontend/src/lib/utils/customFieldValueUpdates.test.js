import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api/items.js', () => ({
  items: { update: vi.fn() },
}));

import { items } from '../api/items.js';
import { updateCustomFieldValue } from './customFieldValueUpdates.js';

// The server merges custom field patches per field: the client sends exactly
// the edited field and the item's other fields survive untouched.
describe('updateCustomFieldValue', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    items.update.mockImplementation(async (id, data) => ({ id, ...data }));
  });

  test('sends only the edited field', async () => {
    const updated = await updateCustomFieldValue(7, '11', 'first');

    expect(items.update).toHaveBeenCalledWith(7, {
      custom_field_values: { 11: 'first' },
    });
    expect(updated).toEqual({ id: 7, custom_field_values: { 11: 'first' } });
  });

  test('clearing a field sends a null value for that field only', async () => {
    await updateCustomFieldValue(7, '11', null);

    expect(items.update).toHaveBeenCalledWith(7, {
      custom_field_values: { 11: null },
    });
  });
});
