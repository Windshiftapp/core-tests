import { describe, expect, test } from 'vitest';
import { parseMarkdownHeadings, slugify } from './markdownToc.js';

describe('parseMarkdownHeadings', () => {
  test('returns an empty array for empty input', () => {
    expect(parseMarkdownHeadings('')).toEqual([]);
    expect(parseMarkdownHeadings(null)).toEqual([]);
    expect(parseMarkdownHeadings(undefined)).toEqual([]);
  });

  test('extracts ATX headings with level, text, slug, and source line', () => {
    const md = '# Title\n\nintro\n\n## Section\n\nbody\n\n### Sub';
    const headings = parseMarkdownHeadings(md);
    expect(headings).toEqual([
      { level: 1, text: 'Title', slug: 'title', line: 0 },
      { level: 2, text: 'Section', slug: 'section', line: 4 },
      { level: 3, text: 'Sub', slug: 'sub', line: 8 },
    ]);
  });

  test('handles all six heading levels', () => {
    const md = '# h1\n## h2\n### h3\n#### h4\n##### h5\n###### h6';
    const headings = parseMarkdownHeadings(md);
    expect(headings.map((h) => h.level)).toEqual([1, 2, 3, 4, 5, 6]);
  });

  test('rejects more than six leading hashes', () => {
    expect(parseMarkdownHeadings('####### too-deep')).toEqual([]);
  });

  test('strips trailing closing hashes (atx-closed form)', () => {
    const md = '# Closed ##\n## Also closed   ##';
    const headings = parseMarkdownHeadings(md);
    expect(headings.map((h) => h.text)).toEqual(['Closed', 'Also closed']);
  });

  test('ignores empty heading text', () => {
    expect(parseMarkdownHeadings('#\n##  \n#### \t')).toEqual([]);
  });

  test('skips headings inside fenced code blocks (backticks and tildes)', () => {
    const md = [
      '# Real heading',
      '',
      '```js',
      '# not a heading',
      '## also not',
      '```',
      '',
      '~~~',
      '### still not',
      '~~~',
      '',
      '## Back to real',
    ].join('\n');
    const headings = parseMarkdownHeadings(md);
    expect(headings.map((h) => h.text)).toEqual(['Real heading', 'Back to real']);
  });

  test('produces stable slugs that survive duplicate text (slug is per-heading)', () => {
    // Note: the parser does NOT disambiguate duplicates — duplicates yield
    // identical slugs. This pins current behavior so a future dedupe
    // strategy can update both the parser and this test together.
    const headings = parseMarkdownHeadings('# Setup\n\n## Setup\n\n### Setup');
    expect(headings.map((h) => h.slug)).toEqual(['setup', 'setup', 'setup']);
  });
});

describe('slugify', () => {
  test('lowercases ASCII letters and digits', () => {
    expect(slugify('Hello WORLD 42')).toBe('hello-world-42');
  });

  test('collapses runs of non-alphanumeric characters to a single dash', () => {
    expect(slugify('foo   bar')).toBe('foo-bar');
    expect(slugify('foo!!!bar???baz')).toBe('foo-bar-baz');
  });

  test('trims leading and trailing dashes', () => {
    expect(slugify('   foo   ')).toBe('foo');
    expect(slugify('!!!foo!!!')).toBe('foo');
  });

  test('removes diacritics so accented headings link cleanly', () => {
    // NFKD decomposes accented letters into base + combining mark, and the
    // combining-mark regex strips the marks — so the base letters survive.
    expect(slugify('Café résumé')).toBe('cafe-resume');
  });

  test('caps output at 80 characters', () => {
    const long = 'a'.repeat(200);
    expect(slugify(long).length).toBeLessThanOrEqual(80);
  });

  test('returns empty string for all-punctuation input', () => {
    expect(slugify('!!!')).toBe('');
    expect(slugify('   ')).toBe('');
  });
});
