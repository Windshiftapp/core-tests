import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { countSilentScopes, findRegressions } from './check-silent-errors.js';

let dir;

async function fixture(files) {
  dir = await mkdtemp(path.join(tmpdir(), 'silent-errors-'));
  const paths = [];
  for (const [name, source] of Object.entries(files)) {
    const full = path.join(dir, name);
    await writeFile(full, source);
    paths.push(full);
  }
  return paths;
}

afterEach(async () => {
  if (dir) await rm(dir, { recursive: true, force: true });
  dir = undefined;
});

describe('countSilentScopes', () => {
  it('flags a catch that only logs', async () => {
    const files = await fixture({
      'a.js': `export function load() {
        try { doThing(); } catch (error) { console.error('Failed to load:', error); }
      }`,
    });

    const counts = await countSilentScopes(files, dir);

    expect(counts.get('a.js')).toBe(1);
  });

  it('accepts a catch that surfaces through surfaceError', async () => {
    const files = await fixture({
      'b.js': `import { surfaceError } from './toasts.js';
        export function save() {
          try { doThing(); } catch (error) { surfaceError(error, { context: 'save' }); }
        }`,
    });

    expect(await countSilentScopes(files, dir)).toEqual(new Map());
  });

  it('accepts a catch that rethrows', async () => {
    const files = await fixture({
      'c.js': `export function save() {
        try { doThing(); } catch (error) { console.error('Failed to save:', error); throw error; }
      }`,
    });

    expect(await countSilentScopes(files, dir)).toEqual(new Map());
  });

  it('accepts a catch that sets an inline error state', async () => {
    const files = await fixture({
      'd.js': `export function save() {
        try { doThing(); } catch (error) { console.error('Failed to save:', error); error = error.message; }
      }`,
    });

    expect(await countSilentScopes(files, dir)).toEqual(new Map());
  });

  it('resolves a local helper that surfaces a toast', async () => {
    const files = await fixture({
      'e.js': `import { errorToast } from './toasts.js';
        function handleError(error) { errorToast(error.message); }
        export function save() {
          try { doThing(); } catch (error) { console.error('Failed to save:', error); handleError(error); }
        }`,
    });

    expect(await countSilentScopes(files, dir)).toEqual(new Map());
  });

  it('flags a promise .catch callback that only logs', async () => {
    const files = await fixture({
      'f.js': `export function save() {
        doThing().catch((error) => console.error('Failed to save:', error));
      }`,
    });

    expect((await countSilentScopes(files, dir)).get('f.js')).toBe(1);
  });
});

describe('findRegressions', () => {
  it('flags new files and files above their baseline', () => {
    const counts = new Map([
      ['known.js', 3],
      ['grew.js', 2],
      ['new.js', 1],
    ]);

    const regressions = findRegressions(counts, { 'known.js': 3, 'grew.js': 1 });

    expect(regressions).toEqual([
      { file: 'grew.js', count: 2, allowed: 1 },
      { file: 'new.js', count: 1, allowed: 0 },
    ]);
  });
});
