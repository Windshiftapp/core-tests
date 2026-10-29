import { existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { resolveConfig } from 'vite';

// Vite's default scan crawls every .html file, so design-system.html and the
// stale dist-design-system build pulled the whole design-system viewer and its
// icon libraries into the app's dep pre-bundle. The app config must scan only
// its own entry.
const APP_ENTRIES = ['index.html'];

// The dev dependency scanner does not walk into route-level lazy-loaded Svelte
// components, so these must be listed explicitly or the first visit re-runs the
// optimizer and reloads.
const LAZY_APP_DEPS = [
  '@xyflow/svelte',
  '@atlaskit/pragmatic-drag-and-drop/element/adapter',
  '@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge',
  '@atlaskit/pragmatic-drag-and-drop-hitbox/tree-item',
  '@atlaskit/pragmatic-drag-and-drop-auto-scroll/element',
  'mermaid',
  'shiki/core',
];

// Icon components are imported per file (WI-1759). Pre-bundling the package
// roots pulls the entire icon set (9 MB Tabler + 2.3 MB Lucide) into the dev
// optimizer output, so they must stay out of the include list.
const ICON_PACKAGE_ROOTS = ['@lucide/svelte', '@tabler/icons-svelte-runes'];

// Only the design-system viewer compares these icon sets; the app never
// imports them.
const DESIGN_SYSTEM_ONLY_DEPS = [
  'phosphor-svelte/lib/BellIcon',
  'carbon-icons-svelte',
  'iconoir',
  '@hugeicons/svelte',
];

// Warmup must cover the app shell and the heaviest route roots so the first
// navigation after a dev-server restart does not pay the compile cost
// (WI-1760). Vite follows static imports recursively from each entry.
const WARMUP_CLIENT_FILES = [
  'src/App.svelte',
  'src/lib/pages/MainApp.svelte',
  'src/lib/features/items/ItemDetail.svelte',
  'src/lib/workspaces/WorkspaceDetail.svelte',
  'src/lib/features/collections/CollectionBoard.svelte',
  'src/lib/features/collections/CollectionList.svelte',
  'src/lib/features/assets/AssetBrowser.svelte',
];

async function resolvedConfig() {
  const root = process.cwd();
  return resolveConfig(
    { root, configFile: resolve(root, 'vite.config.js'), logLevel: 'silent' },
    'serve',
    'development'
  );
}

function resolvedOptimizeDeps() {
  return resolvedConfig().then((config) => config.optimizeDeps);
}

describe('Vite dev dependency optimization', () => {
  it('scans only the app entry, not the design-system viewer', async () => {
    const { entries } = await resolvedOptimizeDeps();
    expect(entries).toEqual(APP_ENTRIES);
  });

  it('pre-bundles deps imported only from lazy Svelte routes', async () => {
    const { include } = await resolvedOptimizeDeps();
    const missing = LAZY_APP_DEPS.filter((dep) => !include.includes(dep));
    expect(missing).toEqual([]);
  });

  it('does not pre-bundle icon package roots', async () => {
    const { include } = await resolvedOptimizeDeps();
    const bundled = ICON_PACKAGE_ROOTS.filter((dep) => include.includes(dep));
    expect(bundled).toEqual([]);
  });

  it('does not pre-bundle design-system-only deps', async () => {
    const { include } = await resolvedOptimizeDeps();
    const leaked = DESIGN_SYSTEM_ONLY_DEPS.filter((dep) => include.includes(dep));
    expect(leaked).toEqual([]);
  });

  it('warms the app shell and heavy route roots at dev startup (WI-1760)', async () => {
    const config = await resolvedConfig();
    const listed = new Set(config.server.warmup?.clientFiles ?? []);
    const missing = WARMUP_CLIENT_FILES.filter((file) => !listed.has(file));
    expect(missing).toEqual([]);
    // Every warmup target must exist so the list cannot silently rot.
    for (const file of config.server.warmup?.clientFiles ?? []) {
      expect(existsSync(resolve(config.root, file)), file).toBe(true);
    }
  });
});
