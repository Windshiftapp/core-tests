import { describe, expect, it } from 'vitest';
import {
  findForbiddenEntryAssets,
  findRootShellBudgetViolations,
  ROOT_SHELL_BUDGET,
} from '../../../scripts/check-entry-assets.js';

describe('findForbiddenEntryAssets', () => {
  it('accepts a core entry with optional features behind dynamic imports', () => {
    const html = `
      <link rel="modulepreload" href="./_app/runtime-abc.js">
      <link rel="stylesheet" href="./_app/index-def.css">
      <script type="module" src="./_app/index-ghi.js"></script>
    `;

    expect(findForbiddenEntryAssets(html)).toEqual([]);
  });

  it.each([
    ['Excalidraw', './_app/excalidraw-abc.js'],
    ['React', './_app/react-dom-abc.js'],
    ['SvelteFlow', './_app/svelteflow-abc.js'],
    ['SvelteFlow', './_app/xyflow-abc.css'],
    ['D3', './_app/d3-abc.js'],
    ['desktop shell', './_app/MainApp-abc.js'],
    ['mobile shell', './_app/MobileShell-abc.js'],
    ['login dialog', './_app/LoginDialog-abc.js'],
    ['setup assistant', './_app/WelcomeAssistant-abc.js'],
    ['portal shell', './_app/Portal-abc.js'],
    ['public form', './_app/PublicFormPage-abc.js'],
    ['public board', './_app/PublicBoard-abc.js'],
    ['print view', './_app/PagePrintView-abc.js'],
    ['password setup', './_app/SetPassword-abc.js'],
  ])('rejects an eager %s asset', (name, asset) => {
    const html = `<link rel="modulepreload" href="${asset}">`;

    expect(findForbiddenEntryAssets(html)).toEqual([{ name, asset }]);
  });

  it('deduplicates assets referenced by more than one entry tag', () => {
    const asset = './_app/svelteflow-abc.js';
    const html = `
      <link rel="modulepreload" href="${asset}">
      <script type="module" src="${asset}"></script>
    `;

    expect(findForbiddenEntryAssets(html)).toEqual([{ name: 'SvelteFlow', asset }]);
  });
});

describe('findRootShellBudgetViolations', () => {
  it('accepts a root chunk within both budgets', () => {
    expect(
      findRootShellBudgetViolations({
        name: 'App-abc.js',
        rawBytes: ROOT_SHELL_BUDGET.rawBytes,
        gzipBytes: ROOT_SHELL_BUDGET.gzipBytes,
      })
    ).toEqual([]);
  });

  it('reports raw and gzip regressions independently', () => {
    expect(
      findRootShellBudgetViolations(
        { name: 'App-abc.js', rawBytes: 101, gzipBytes: 51 },
        { rawBytes: 100, gzipBytes: 50 }
      )
    ).toEqual([
      { metric: 'raw', actual: 101, limit: 100 },
      { metric: 'gzip', actual: 51, limit: 50 },
    ]);
  });
});
