import { spawnSync } from 'node:child_process';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import {
  ENTRY_ALLOWLIST,
  buildImporterGraph,
  extractImportSpecifiers,
  findBoundaryViolations,
  findStaleAllowlistEntries,
  resolveSpecifier,
} from './check-hmr-boundaries.js';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const realSrcDir = path.resolve(scriptDir, '../src');
const scriptPath = path.resolve(scriptDir, 'check-hmr-boundaries.js');

let fixtureDirs = [];

async function createFixture(files) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'hmr-guard-'));
  fixtureDirs.push(root);
  for (const [relativePath, contents] of Object.entries(files)) {
    const filePath = path.join(root, relativePath);
    await mkdir(path.dirname(filePath), { recursive: true });
    await writeFile(filePath, contents);
  }
  return root;
}

async function violationsFor(files, options = {}) {
  const srcDir = await createFixture(files);
  const importers = await buildImporterGraph({ srcDir });
  return findBoundaryViolations(importers, { srcDir, ...options });
}

afterEach(async () => {
  await Promise.all(fixtureDirs.map((dir) => rm(dir, { recursive: true, force: true })));
  fixtureDirs = [];
});

describe('extractImportSpecifiers', () => {
  it('collects static, re-export, side-effect and literal dynamic imports', () => {
    const source = `
      import './app.css';
      import { a } from './a.js';
      import type { B } from './b.ts';
      export { c } from './c.js';
      export * from './d.js';
      const lazy = () => import('./e.js');
      const dynamic = (name) => import(\`./locales/\${name}.js\`);
    `;

    expect(extractImportSpecifiers(source)).toEqual([
      './app.css',
      './a.js',
      './b.ts',
      './c.js',
      './d.js',
      './e.js',
    ]);
  });

  it('ignores commented-out imports and bare package imports', () => {
    const source = `
      import { writable } from 'svelte/store';
      // import { old } from './commented.js';
      /* import { block } from './block.js'; */
    `;

    expect(extractImportSpecifiers(source)).toEqual(['svelte/store']);
  });
});

describe('resolveSpecifier', () => {
  it('resolves extensionless, index and component imports', async () => {
    const srcDir = await createFixture({
      'lib/a.js': '',
      'lib/b/index.js': '',
      'lib/c.svelte': '',
    });
    const fromFile = path.join(srcDir, 'lib/a.js');

    await expect(resolveSpecifier(fromFile, './b')).resolves.toBe(path.join(srcDir, 'lib/b/index.js'));
    await expect(resolveSpecifier(fromFile, './c.svelte')).resolves.toBe(
      path.join(srcDir, 'lib/c.svelte')
    );
    await expect(resolveSpecifier(fromFile, './b/index.js?raw')).resolves.toBe(
      path.join(srcDir, 'lib/b/index.js')
    );
  });

  it('ignores package imports and unresolvable paths', async () => {
    const srcDir = await createFixture({ 'main.js': '' });
    const fromFile = path.join(srcDir, 'main.js');

    await expect(resolveSpecifier(fromFile, 'svelte')).resolves.toBeNull();
    await expect(resolveSpecifier(fromFile, './missing.js')).resolves.toBeNull();
  });
});

describe('findBoundaryViolations', () => {
  it('reports a non-Svelte module imported directly by the entry', async () => {
    const violations = await violationsFor({
      'main.js': "import { route } from './lib/router.js';",
      'lib/router.js': 'export const route = null;',
    });

    expect(violations).toEqual([
      { file: 'lib/router.js', chain: ['lib/router.js', 'main.js'] },
    ]);
  });

  it('stops propagation at a Svelte component boundary', async () => {
    const violations = await violationsFor({
      'main.js': "import Shell from './lib/Shell.svelte';",
      'lib/Shell.svelte': "<script>import './router.js';</script>",
      'lib/router.js': 'export const route = null;',
    });

    expect(violations).toEqual([]);
  });

  it('reports the legacy dynamic-import chain through a Svelte runes store', async () => {
    // Mirrors the regression this guard exists for: main.js dynamically imported
    // a store module, which pulled the router in behind the entry.
    const violations = await violationsFor({
      'main.js': "await import('./lib/stores/collectionContext.js');",
      'lib/stores/collectionContext.js': "export * from './collectionContext.svelte.js';",
      'lib/stores/collectionContext.svelte.js': "import { route } from '../router.js';",
      'lib/router.js': 'export const route = null;',
      'lib/features/View.svelte': "<script>import { route } from '../router.js';</script>",
    });

    const router = violations.find((violation) => violation.file === 'lib/router.js');
    expect(router.chain).toEqual([
      'lib/router.js',
      'lib/stores/collectionContext.svelte.js',
      'lib/stores/collectionContext.js',
      'main.js',
    ]);
    // The store modules themselves are behind the entry too.
    expect(violations.map((violation) => violation.file)).toEqual([
      'lib/router.js',
      'lib/stores/collectionContext.js',
      'lib/stores/collectionContext.svelte.js',
    ]);
  });

  it('never reports the entry or Svelte components', async () => {
    const violations = await violationsFor({
      'main.js': "import './lib/App.svelte';",
      'lib/App.svelte': "<script>import './router.js';</script>",
      'lib/router.js': '',
    });

    expect(violations).toEqual([]);
  });

  it('suppresses allowlisted entry modules and reports stale entries', async () => {
    const srcDir = await createFixture({
      'main.js': "import './lib/boot.js';",
      'lib/boot.js': '',
      'lib/router.js': '',
    });
    const importers = await buildImporterGraph({ srcDir });
    const allowlist = [{ file: 'lib/boot.js', reason: 'runs before the app chunk' }];

    expect(findBoundaryViolations(importers, { srcDir, allowlist })).toEqual([]);
    expect(findStaleAllowlistEntries(importers, { srcDir, allowlist })).toEqual([]);

    const staleAllowlist = [{ file: 'lib/router.js', reason: 'no longer needed' }];
    expect(findStaleAllowlistEntries(importers, { srcDir, allowlist: staleAllowlist })).toHaveLength(
      1
    );
  });
});

describe('check-hmr-boundaries CLI', () => {
  it('exits non-zero and prints the offending chain', async () => {
    const srcDir = await createFixture({
      'main.js': "import { route } from './lib/router.js';",
      'lib/router.js': '',
    });

    const result = spawnSync(process.execPath, [scriptPath, srcDir], { encoding: 'utf8' });

    expect(result.status).toBe(1);
    expect(result.stderr).toContain('lib/router.js <- main.js');
    expect(result.stderr).toContain('ENTRY_ALLOWLIST');
  });

  it('passes when every module sits behind a Svelte boundary', async () => {
    const srcDir = await createFixture({
      // The allowlisted entry modules exist and are genuinely needed here.
      'main.js': [
        "import './lib/runtime/contextPath.js';",
        "import './lib/mobile/serviceWorkerClient.js';",
        "import App from './App.svelte';",
      ].join('\n'),
      'lib/runtime/contextPath.js': '',
      'lib/mobile/serviceWorkerClient.js': '',
      'App.svelte': "<script>import './lib/router.js';</script>",
      'lib/router.js': '',
    });

    const result = spawnSync(process.execPath, [scriptPath, srcDir], { encoding: 'utf8' });

    expect(result.status).toBe(0);
    expect(result.stdout).toContain('HMR boundary guard passed');
  });
});

describe('the repository tree', () => {
  it('has no module that can reach the entry without a Svelte boundary', async () => {
    const importers = await buildImporterGraph({ srcDir: realSrcDir });
    const violations = findBoundaryViolations(importers, {
      srcDir: realSrcDir,
      allowlist: ENTRY_ALLOWLIST,
    });
    const stale = findStaleAllowlistEntries(importers, {
      srcDir: realSrcDir,
      allowlist: ENTRY_ALLOWLIST,
    });

    // A suspiciously small graph means the scanner stopped early.
    expect(importers.size).toBeGreaterThan(900);
    expect(violations).toEqual([]);
    expect(stale).toEqual([]);
    for (const entry of ENTRY_ALLOWLIST) {
      expect(importers.has(path.join(realSrcDir, entry.file))).toBe(true);
    }
  });
});
