import { describe, expect, it } from 'vitest';
import {
  escapeHtml,
  safeRelativeRedirectPath,
  sanitizeHtml,
  sanitizeMarkdownHtml,
  stripHtml,
} from './sanitize';

describe('sanitizeHtml', () => {
  it('strips script tags', () => {
    expect(sanitizeHtml('<script>alert(1)</script>')).toBe('');
  });

  it('strips event handlers', () => {
    expect(sanitizeHtml('<img src="x" onerror="alert(1)">')).toBe('<img src="x">');
  });

  it('removes javascript: hrefs', () => {
    expect(sanitizeHtml('<a href="javascript:alert(1)">click</a>')).toBe('<a>click</a>');
  });

  it('removes data: URIs on anchors', () => {
    expect(sanitizeHtml('<a href="data:text/html,<script>alert(1)</script>">click</a>')).toBe(
      '<a>click</a>'
    );
  });

  it('preserves safe formatting tags', () => {
    const input = '<strong>bold</strong> <em>italic</em> <a href="https://example.com">link</a>';
    expect(sanitizeHtml(input)).toBe(
      '<strong>bold</strong> <em>italic</em> <a href="https://example.com">link</a>'
    );
  });

  it('preserves list elements', () => {
    const input = '<ul><li>item 1</li><li>item 2</li></ul>';
    expect(sanitizeHtml(input)).toBe(input);
  });

  it('preserves code blocks', () => {
    const input = '<pre><code>const x = 1;</code></pre>';
    expect(sanitizeHtml(input)).toBe(input);
  });

  it('preserves img tags with safe attributes', () => {
    expect(sanitizeHtml('<img src="photo.jpg" alt="photo">')).toBe(
      '<img src="photo.jpg" alt="photo">'
    );
  });

  it('strips onclick from div', () => {
    expect(sanitizeHtml('<div onclick="alert(1)">text</div>')).toBe('<div>text</div>');
  });

  it('strips iframe tags', () => {
    expect(sanitizeHtml('<iframe src="evil.com"></iframe>')).toBe('');
  });

  it('handles null/undefined/empty', () => {
    expect(sanitizeHtml('')).toBe('');
    expect(sanitizeHtml(null as unknown as string)).toBe('');
    expect(sanitizeHtml(undefined as unknown as string)).toBe('');
  });

  it('preserves SVG elements', () => {
    const input =
      '<svg viewBox="0 0 24 24" width="16" height="16"><circle cx="12" cy="12" r="10" fill="green"></circle></svg>';
    expect(sanitizeHtml(input)).toBe(input);
  });

  it('removes dangerous SVG and MathML payloads', () => {
    expect(
      sanitizeHtml('<svg><script>alert(1)</script><circle onload="alert(1)"></circle></svg>')
    ).toBe('<svg><circle></circle></svg>');
    expect(sanitizeHtml('<math><mtext><img src=x onerror=alert(1)></mtext></math>')).toBe('');
  });

  it('removes template payloads', () => {
    expect(sanitizeHtml('<template><img src=x onerror=alert(1)></template><p>safe</p>')).toBe(
      '<p>safe</p>'
    );
  });

  it('isolates sanitizer configuration between calls', () => {
    expect(sanitizeHtml('<a data-secret="x" onclick="alert(1)">first</a>')).toBe('<a>first</a>');
    expect(sanitizeHtml('<a data-secret="x" onclick="alert(1)">second</a>')).toBe('<a>second</a>');
  });
});

describe('stripHtml', () => {
  it('strips all HTML tags', () => {
    expect(stripHtml('<b>Bold</b> and <i>italic</i>')).toBe('Bold and italic');
  });

  it('strips script tags and content', () => {
    expect(stripHtml('<script>alert(1)</script>text')).toBe('text');
  });

  it('strips event handlers', () => {
    expect(stripHtml('<img src="x" onerror="alert(1)">')).toBe('');
  });

  it('handles null/undefined/empty', () => {
    expect(stripHtml('')).toBe('');
    expect(stripHtml(null as unknown as string)).toBe('');
    expect(stripHtml(undefined as unknown as string)).toBe('');
  });
});

describe('sanitizeMarkdownHtml', () => {
  it('removes an unsafe server response independently of Markdown parsing', () => {
    const dirty =
      '<p>safe</p><script>alert(1)</script><img src="x" onerror="alert(2)"><a href="javascript:alert(3)">bad</a>';
    expect(sanitizeMarkdownHtml(dirty)).toBe('<p>safe</p><img src="x"><a>bad</a>');
  });

  it('keeps supported server-rendered Markdown', () => {
    const rendered =
      '<h2>Heading</h2><p><code>Promise&lt;Anything&gt;</code> <a href="page:185">plan</a></p>';
    expect(sanitizeMarkdownHtml(rendered)).toBe(rendered);
  });

  it('allows raster data images but rejects SVG data images and data links', () => {
    const png = 'data:image/png;base64,iVBORw0KGgo=';
    expect(sanitizeMarkdownHtml(`<img src="${png}" alt="safe">`)).toBe(
      `<img src="${png}" alt="safe">`
    );
    expect(sanitizeMarkdownHtml('<img src="data:image/svg+xml;base64,PHN2Zz4=" alt="x">')).toBe(
      '<img alt="x">'
    );
    expect(sanitizeMarkdownHtml(`<a href="${png}">image</a>`)).toBe('<a>image</a>');
  });

  it('accepts only numeric page links and rejects external-looking relative links', () => {
    expect(sanitizeMarkdownHtml('<a href="page:185">plan</a>')).toBe('<a href="page:185">plan</a>');
    expect(sanitizeMarkdownHtml('<a href="page:javascript">bad page</a>')).toBe('<a>bad page</a>');
    expect(sanitizeMarkdownHtml('<a href="//evil.example/path">bad host</a>')).toBe(
      '<a>bad host</a>'
    );
    expect(sanitizeMarkdownHtml('<a href="\\evil.example/path">bad slash</a>')).toBe(
      '<a>bad slash</a>'
    );
  });
});

describe('safeRelativeRedirectPath', () => {
  it('allows relative paths with query strings', () => {
    expect(
      safeRelativeRedirectPath('/cli/authorize?state=abc&callback=http%3A%2F%2F127.0.0.1')
    ).toBe('/cli/authorize?state=abc&callback=http%3A%2F%2F127.0.0.1');
  });

  it('trims surrounding whitespace', () => {
    expect(safeRelativeRedirectPath('  /workspaces  ')).toBe('/workspaces');
  });

  it('rejects empty, absolute, protocol-relative, and malformed paths', () => {
    expect(safeRelativeRedirectPath('')).toBe('');
    expect(safeRelativeRedirectPath('https://example.com')).toBe('');
    expect(safeRelativeRedirectPath('//example.com')).toBe('');
    expect(safeRelativeRedirectPath('/\\example.com')).toBe('');
    expect(safeRelativeRedirectPath('/path\\segment')).toBe('');
    expect(safeRelativeRedirectPath('/@example.com')).toBe('');
    expect(safeRelativeRedirectPath('/path\nnext')).toBe('');
  });
});

describe('escapeHtml', () => {
  it('escapes HTML entities', () => {
    expect(escapeHtml('<script>alert("xss")</script>')).toBe(
      '&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;'
    );
  });

  it('escapes ampersands', () => {
    expect(escapeHtml('foo & bar')).toBe('foo &amp; bar');
  });

  it('escapes single quotes', () => {
    expect(escapeHtml("it's")).toBe('it&#39;s');
  });

  it('handles null/undefined', () => {
    expect(escapeHtml(null)).toBe('');
    expect(escapeHtml(undefined)).toBe('');
  });

  it('handles numbers', () => {
    expect(escapeHtml(42)).toBe('42');
  });

  it('handles plain text unchanged', () => {
    expect(escapeHtml('hello world')).toBe('hello world');
  });
});
