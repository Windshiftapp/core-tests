import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

const readJson = (path) => JSON.parse(readFileSync(resolve(process.cwd(), path), 'utf8'));

class TestCSSStyleSheet {
  cssRules = [];

  insertRule(rule, index = this.cssRules.length) {
    this.cssRules.splice(index, 0, { cssText: rule });
    return index;
  }

  replaceSync(css) {
    this.cssRules = [{ cssText: css }];
  }
}

describe('Mermaid dependency alignment', () => {
  it('uses a patched Mermaid 11 release without forcing Excalidraw back to Mermaid 10', () => {
    const packageJson = readJson('package.json');
    const packageLock = readJson('package-lock.json');
    const resolvedMermaid = packageLock.packages['node_modules/mermaid'];

    expect(packageJson.dependencies.mermaid).toBe('^11.16.1');
    expect(packageJson.overrides.mermaid).toBeUndefined();
    expect(packageJson.overrides['@excalidraw/mermaid-to-excalidraw'].mermaid).toBeUndefined();
    expect(resolvedMermaid.version).toBe('11.16.1');
    expect(
      packageLock.packages['node_modules/@excalidraw/mermaid-to-excalidraw'].dependencies.mermaid
    ).toBe('^11.12.1');
  });

  it('renders ordinary diagrams while strict mode neutralizes executable label markup', async () => {
    const originalCSSStyleSheet = globalThis.CSSStyleSheet;
    const getBBox = Object.getOwnPropertyDescriptor(SVGElement.prototype, 'getBBox');
    const getComputedTextLength = Object.getOwnPropertyDescriptor(
      SVGElement.prototype,
      'getComputedTextLength'
    );

    globalThis.CSSStyleSheet = TestCSSStyleSheet;
    Object.defineProperty(SVGElement.prototype, 'getBBox', {
      configurable: true,
      value: () => ({ x: 0, y: 0, width: 100, height: 20 }),
    });
    Object.defineProperty(SVGElement.prototype, 'getComputedTextLength', {
      configurable: true,
      value: () => 100,
    });

    try {
      const { default: mermaid } = await import('mermaid');
      mermaid.initialize({ startOnLoad: false, securityLevel: 'strict' });

      const flowchart = await mermaid.render(
        'mermaid-flowchart-probe',
        'flowchart TD\nA[Hello] --> B[World]'
      );
      expect(flowchart.svg).toContain('Hello');
      expect(flowchart.svg).toContain('World');

      const sequence = await mermaid.render(
        'mermaid-security-probe',
        'sequenceDiagram\nA->>B: <img src=x onerror=alert(1)>'
      );
      const host = document.createElement('div');
      host.innerHTML = sequence.svg;

      expect(host.querySelectorAll('script, [onerror], [onclick]')).toHaveLength(0);
      expect(host.querySelectorAll('img')).toHaveLength(0);

      const { parseMermaidToExcalidraw } = await import('@excalidraw/mermaid-to-excalidraw');
      const excalidrawScene = await parseMermaidToExcalidraw('flowchart TD\nA[Hello] --> B[World]');
      expect(excalidrawScene.elements.length).toBeGreaterThan(0);
    } finally {
      globalThis.CSSStyleSheet = originalCSSStyleSheet;
      restoreProperty(SVGElement.prototype, 'getBBox', getBBox);
      restoreProperty(SVGElement.prototype, 'getComputedTextLength', getComputedTextLength);
    }
  });
});

function restoreProperty(target, property, descriptor) {
  if (descriptor) {
    Object.defineProperty(target, property, descriptor);
    return;
  }
  delete target[property];
}
