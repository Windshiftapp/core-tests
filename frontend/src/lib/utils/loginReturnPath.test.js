import { describe, expect, it } from 'vitest';

import { safeLoginReturnPath } from './loginReturnPath.js';

describe('safeLoginReturnPath', () => {
  it('preserves a local form path and its query string', () => {
    expect(safeLoginReturnPath('?return_to=%2Fforms%2Fsupport%2F7%3Fsource%3Demail')).toBe(
      '/forms/support/7?source=email'
    );
  });

  it.each([
    ['an absolute URL', '?return_to=https%3A%2F%2Fevil.example%2Fforms'],
    ['a protocol-relative URL', '?return_to=%2F%2Fevil.example%2Fforms'],
    ['a backslash-relative URL', '?return_to=%2F%5Cevil.example%2Fforms'],
    ['the login route', '?return_to=%2Flogin%3Freturn_to%3D%252Fforms'],
    ['a whitespace-obscured path', '?return_to=%20%2Fforms%2Fsupport'],
  ])('rejects %s', (_description, search) => {
    expect(safeLoginReturnPath(search)).toBe('');
  });
});
