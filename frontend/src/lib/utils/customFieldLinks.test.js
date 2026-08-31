import { describe, expect, test } from 'vitest';
import { customFieldLinkHref } from './customFieldLinks.js';

describe('customFieldLinkHref', () => {
  test.each([
    ['javascript:alert(1)', 'url'],
    ['data:text/html,<script>alert(1)</script>', 'url'],
    ['vbscript:msgbox(1)', 'url'],
    ['//evil.example/path', 'url'],
  ])('rejects %s in a legacy URL field', (value, fieldType) => {
    expect(customFieldLinkHref(fieldType, value)).toBeNull();
  });

  test('links an HTTPS value stored in a text field', () => {
    expect(customFieldLinkHref('text', 'https://example.com/docs')).toBe(
      'https://example.com/docs'
    );
  });

  test('does not link non-HTTP text values', () => {
    expect(customFieldLinkHref('text', 'javascript:alert(1)')).toBeNull();
  });
});
