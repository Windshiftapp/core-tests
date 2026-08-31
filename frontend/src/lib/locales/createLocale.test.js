import { describe, expect, it } from 'vitest';
import { createLocale } from './createLocale.js';

describe('createLocale', () => {
  it('merges nested locale modules', () => {
    const locale = createLocale({
      common: { actions: { save: 'Save' } },
      dialogs: { actions: { cancel: 'Cancel' } },
    });

    expect(locale).toEqual({
      actions: {
        save: 'Save',
        cancel: 'Cancel',
      },
    });
  });

  it('ignores keys that can modify object prototypes', () => {
    const source = JSON.parse(
      '{"safe":{"label":"Safe","__proto__":{"polluted":true}},"constructor":{"prototype":{"polluted":true}}}'
    );

    try {
      const locale = createLocale({ source });

      expect(Object.prototype.polluted).toBeUndefined();
      expect(locale).toEqual({ safe: { label: 'Safe' } });
    } finally {
      delete Object.prototype.polluted;
    }
  });
});
