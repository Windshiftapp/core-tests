import { describe, expect, it } from 'vitest';
import {
  buildFormSteps,
  clampFormStep,
  initializeFormValues,
  parseFormOptions,
  validateFormStep,
} from './formModel.js';

const fields = [
  {
    field_identifier: 'title',
    field_type: 'default',
    display_name: 'Summary',
    is_required: true,
    step_number: 1,
  },
  {
    field_identifier: '42',
    field_type: 'custom',
    display_name: 'Impact',
    is_required: true,
    step_number: 2,
  },
  {
    field_identifier: 'confirmed',
    field_type: 'virtual',
    virtual_field_type: 'checkbox',
    display_name: 'I confirm',
    is_required: true,
    step_number: 2,
  },
  {
    field_identifier: '43',
    field_type: 'custom',
    display_name: 'Approved',
    is_required: true,
    step_number: 2,
  },
];

const customFieldDefinitions = [{ id: 43, field_type: 'boolean', name: 'Approved' }];

describe('shared form model', () => {
  it('initializes default, custom, and virtual values and preserves supplied values', () => {
    expect(
      initializeFormValues(
        fields,
        {
          title: 'Printer issue',
          custom_fields: { 42: 'Office blocked', confirmed: true },
        },
        customFieldDefinitions
      )
    ).toEqual({
      formData: { title: 'Printer issue', description: '' },
      customFieldValues: { 42: 'Office blocked', confirmed: true, 43: false },
    });
  });

  it('derives and clamps multi-step navigation', () => {
    expect(buildFormSteps(fields)).toEqual([1, 2]);
    expect(clampFormStep([1, 2], 2)).toBe(2);
    expect(clampFormStep([1, 2], 9)).toBe(1);
  });

  it('returns the exact first missing required field for a step', () => {
    expect(
      validateFormStep({
        fields,
        step: 2,
        formData: { title: 'Ready', description: '' },
        customFieldValues: { 42: '', confirmed: false, 43: false },
        customFieldDefinitions,
      })
    ).toBe('Impact is required');

    expect(
      validateFormStep({
        fields,
        step: 2,
        formData: { title: 'Ready', description: '' },
        customFieldValues: { 42: 'High', confirmed: false, 43: false },
        customFieldDefinitions,
      })
    ).toBe('');

    expect(
      validateFormStep({
        fields,
        step: 2,
        formData: { title: 'Ready', description: '' },
        customFieldValues: { 42: 'High', confirmed: false },
        customFieldDefinitions,
      })
    ).toBe('');
  });

  it('requires a configured title even when its schema flag is false', () => {
    const optionalTitle = [{ ...fields[0], is_required: false }];
    expect(
      validateFormStep({
        fields: optionalTitle,
        step: 1,
        formData: { title: '   ', description: '' },
        customFieldValues: {},
      })
    ).toBe('Summary is required');
  });

  it('normalizes virtual select options and rejects malformed JSON', () => {
    expect(parseFormOptions('[{"value":"high","label":"High"}]')).toEqual([
      { value: 'high', label: 'High' },
    ]);
    expect(parseFormOptions('{bad')).toEqual([]);
  });
});
