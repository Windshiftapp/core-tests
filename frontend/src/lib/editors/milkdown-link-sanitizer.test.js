import { describe, expect, it } from 'vitest';
import { isSafeImageUrl, isSafeUrl } from './milkdown-link-sanitizer.js';

describe('isSafeUrl', () => {
  describe('safe URLs', () => {
    it('allows https URLs', () => {
      expect(isSafeUrl('https://example.com')).toBe(true);
    });

    it('allows http URLs', () => {
      expect(isSafeUrl('http://example.com')).toBe(true);
    });

    it('allows mailto URLs', () => {
      expect(isSafeUrl('mailto:test@example.com')).toBe(true);
    });

    it('allows tel URLs', () => {
      expect(isSafeUrl('tel:+1234567890')).toBe(true);
    });

    it('allows Windshift page links', () => {
      expect(isSafeUrl('page:123')).toBe(true);
    });

    it('allows fragment-only URLs', () => {
      expect(isSafeUrl('#section')).toBe(true);
    });

    it('allows relative URLs', () => {
      expect(isSafeUrl('/about')).toBe(true);
    });

    it('allows relative paths without leading slash', () => {
      expect(isSafeUrl('about/page')).toBe(true);
    });

    it('allows empty strings', () => {
      expect(isSafeUrl('')).toBe(true);
    });

    it('allows null/undefined', () => {
      expect(isSafeUrl(null)).toBe(true);
      expect(isSafeUrl(undefined)).toBe(true);
    });
  });

  describe('dangerous URLs', () => {
    it('blocks javascript: URLs', () => {
      expect(isSafeUrl('javascript:alert(1)')).toBe(false);
    });

    it('blocks javascript: case-insensitive', () => {
      expect(isSafeUrl('JaVaScRiPt:alert(1)')).toBe(false);
    });

    it('blocks javascript: with leading spaces', () => {
      expect(isSafeUrl('  javascript:alert(1)')).toBe(false);
    });

    it('blocks vbscript: URLs', () => {
      expect(isSafeUrl('vbscript:MsgBox("xss")')).toBe(false);
    });

    it('blocks data: URLs', () => {
      expect(isSafeUrl('data:text/html,<script>alert(1)</script>')).toBe(false);
    });

    it('blocks data: image URLs (SVG XSS)', () => {
      expect(isSafeUrl('data:image/svg+xml,<svg onload=alert(1)>')).toBe(false);
    });

    it('blocks protocol-relative URLs', () => {
      expect(isSafeUrl('//evil.com/path')).toBe(false);
    });

    it('blocks protocol-relative URLs with leading whitespace', () => {
      expect(isSafeUrl('  //evil.com')).toBe(false);
    });

    it('blocks blob URLs for regular links', () => {
      expect(isSafeUrl('blob:https://example.com/local-preview')).toBe(false);
    });

    it('blocks malformed page and encoded-backslash URLs', () => {
      expect(isSafeUrl('page:javascript')).toBe(false);
      expect(isSafeUrl('/%5cevil.example/path')).toBe(false);
    });
  });
});

describe('isSafeImageUrl', () => {
  it('allows blob URLs for local image previews', () => {
    expect(isSafeImageUrl('blob:https://example.com/local-preview')).toBe(true);
  });

  it('still blocks dangerous image URLs', () => {
    expect(isSafeImageUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeImageUrl('data:image/svg+xml,<svg onload=alert(1)>')).toBe(false);
    expect(isSafeImageUrl('//evil.com/path')).toBe(false);
  });

  it('allows the raster data images accepted by readonly Markdown', () => {
    expect(isSafeImageUrl('data:image/png;base64,iVBORw0KGgo=')).toBe(true);
  });
});
