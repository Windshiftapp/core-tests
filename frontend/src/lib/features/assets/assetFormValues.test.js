import { describe, expect, it } from 'vitest';
import { retainValuesForType } from './assetFormValues.js';

describe('retainValuesForType', () => {
  it('keeps compatible ID and name keys while pruning values from the old type', () => {
    const values = {
      10: 'old',
      20: 'shared by id',
      Location: 'shared by name',
      location: 'shared by lower-case name',
    };
    const fields = [
      { custom_field_id: 20, field_name: 'Shared' },
      { custom_field_id: 30, field_name: 'Location', is_required: true },
    ];

    expect(retainValuesForType(values, fields)).toEqual({
      20: 'shared by id',
      Location: 'shared by name',
      location: 'shared by lower-case name',
    });
    expect(values).toHaveProperty('10', 'old');
  });
});
