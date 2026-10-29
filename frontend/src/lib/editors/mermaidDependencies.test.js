import { describe, expect, it } from 'vitest';
import { parseMermaidToExcalidrawElements } from './mermaidConversion.js';

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

// Mermaid measures rendered SVG geometry, which jsdom does not implement.
// Stub the layout APIs and CSSOM for the duration of the callback.
async function withMermaidDomStubs(fn) {
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
    return await fn();
  } finally {
    globalThis.CSSStyleSheet = originalCSSStyleSheet;
    restoreProperty(SVGElement.prototype, 'getBBox', getBBox);
    restoreProperty(SVGElement.prototype, 'getComputedTextLength', getComputedTextLength);
  }
}

describe('Mermaid dependency alignment', () => {
  it('renders ordinary diagrams while strict mode neutralizes executable label markup', async () => {
    await withMermaidDomStubs(async () => {
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
    });
  });

  it('converts flowchart subgraphs into editable elements instead of a fallback image', async () => {
    await withMermaidDomStubs(async () => {
      // Mermaid >= 11.14 prefixes rendered element ids with the render id. The
      // converter looks subgraphs up by their bare id, so without the id fix it
      // throws "SubGraph element not found" and returns a single static image.
      const source = [
        'flowchart LR',
        '  subgraph Control',
        '    Fleet[Fleet controller<br/>inventory]',
        '  end',
        '  subgraph Nodes',
        '    ActiveA[Active tenant stack]',
        '  end',
        '  Fleet --> ActiveA',
      ].join('\n');

      const scene = await parseMermaidToExcalidrawElements(source);

      expect(scene.elements.length).toBeGreaterThan(1);
      expect(scene.elements.filter((element) => element.type === 'image')).toHaveLength(0);
      const texts = scene.elements.flatMap((element) =>
        [element.text, element.label?.text].filter((text) => typeof text === 'string')
      );
      expect(texts).toContain('Fleet controller\ninventory');
      expect(texts).toContain('Active tenant stack');
    });
  });
});

function restoreProperty(target, property, descriptor) {
  if (descriptor) {
    Object.defineProperty(target, property, descriptor);
    return;
  }
  delete target[property];
}
