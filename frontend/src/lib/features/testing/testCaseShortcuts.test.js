import { describe, expect, it } from 'vitest';
import { createStepsShortcutCodes, STEPS_SHORTCUT_ALPHABET } from './testCaseShortcuts.js';

describe('createStepsShortcutCodes', () => {
  it('assigns a shortcut to every row beyond the original nine-row limit', () => {
    expect(createStepsShortcutCodes(12)).toEqual([
      '1',
      '2',
      '3',
      '4',
      '5',
      '6',
      '7',
      '8',
      '9',
      '0',
      'A',
      'B',
    ]);
  });

  it('creates unique fixed-width codes when a list exceeds the alphabet', () => {
    const codes = createStepsShortcutCodes(STEPS_SHORTCUT_ALPHABET.length + 1);

    expect(new Set(codes).size).toBe(codes.length);
    expect(codes.every((code) => code.length === 2)).toBe(true);
    expect(codes.at(-1)).toBe('21');
  });

  it('returns no codes for an empty or invalid count', () => {
    expect(createStepsShortcutCodes(0)).toEqual([]);
    expect(createStepsShortcutCodes(-1)).toEqual([]);
    expect(createStepsShortcutCodes(1.5)).toEqual([]);
  });
});
