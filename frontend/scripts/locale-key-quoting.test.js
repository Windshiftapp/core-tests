import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { findMixedKeyQuoting } from './locale-key-quoting.js';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const localeRoot = path.resolve(scriptDir, '../src/lib/locales');

function source(content, file = 'src/lib/locales/xx/supplemental.js') {
  return { file, content };
}

async function readLocaleSources() {
  const entries = await readdir(localeRoot, { withFileTypes: true });
  const files = [];

  for (const entry of entries.filter((e) => e.isDirectory())) {
    const dir = path.join(localeRoot, entry.name);
    const names = (await readdir(dir)).filter((f) => f.endsWith('.js') && f !== 'index.js');
    for (const name of names) {
      files.push({
        file: `src/lib/locales/${entry.name}/${name}`,
        content: await readFile(path.join(dir, name), 'utf-8'),
      });
    }
  }

  return files;
}

describe('findMixedKeyQuoting', () => {
  it('flags a bare identifier key in a file that quotes identifier keys', () => {
    const content = [
      'export default {',
      '  "title": "Sprache",',
      '  languageTitle: "Sprache",',
      '  "saved": "Gespeichert",',
      '};',
    ].join('\n');

    expect(findMixedKeyQuoting([source(content)])).toEqual([
      {
        file: 'src/lib/locales/xx/supplemental.js',
        unquoted: [{ line: 3, key: 'languageTitle' }],
      },
    ]);
  });

  it('accepts a file whose identifier keys are all quoted', () => {
    const content = ['export default {', '  "title": "Sprache",', '  "saved": "Gespeichert",', '};'].join(
      '\n'
    );

    expect(findMixedKeyQuoting([source(content)])).toEqual([]);
  });

  it('accepts an unquoted-key file that only quotes keys that require it', () => {
    const content = [
      'export default {',
      '  title: "Sprache",',
      '  saved: "Gespeichert",',
      '  "field-mappings": "Zuordnungen",',
      '};',
    ].join('\n');

    expect(findMixedKeyQuoting([source(content)])).toEqual([]);
  });

  it('reports every offending line across every file', () => {
    const first = source(
      ['export default {', '  "a": "x",', '  badOne: "y",', '  badTwo: "z",', '};'].join('\n'),
      'src/lib/locales/aa/a.js'
    );
    const second = source(
      ['export default {', '  "b": "x",', '  badThree: "y",', '};'].join('\n'),
      'src/lib/locales/bb/b.js'
    );

    expect(findMixedKeyQuoting([first, second])).toEqual([
      {
        file: 'src/lib/locales/aa/a.js',
        unquoted: [
          { line: 3, key: 'badOne' },
          { line: 4, key: 'badTwo' },
        ],
      },
      { file: 'src/lib/locales/bb/b.js', unquoted: [{ line: 3, key: 'badThree' }] },
    ]);
  });
});

describe('shipped locale catalogs', () => {
  it('never mix quoted and unquoted identifier keys', async () => {
    expect(findMixedKeyQuoting(await readLocaleSources())).toEqual([]);
  });
});
