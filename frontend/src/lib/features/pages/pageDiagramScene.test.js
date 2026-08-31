import { describe, expect, it, vi } from 'vitest';
import {
  decodePageDiagramPayload,
  PageDiagramSceneError,
  pageDiagramSceneFingerprint,
  preparePageDiagramScene,
} from './pageDiagramScene.js';

describe('Page diagram scene payloads', () => {
  it('recognizes and converts a Mermaid seed into editable Excalidraw data', async () => {
    const parseMermaid = vi.fn().mockResolvedValue({
      elements: [{ id: 'skeleton' }],
      files: { image: { id: 'image' } },
    });
    const convertElements = vi
      .fn()
      .mockReturnValue([{ id: 'converted', type: 'rectangle', x: 10, y: 20 }]);

    const scene = await preparePageDiagramScene(
      { type: 'mermaid', source: ' graph TD; A-->B ' },
      { parseMermaid, convertElements }
    );

    expect(parseMermaid).toHaveBeenCalledWith('graph TD; A-->B');
    expect(convertElements).toHaveBeenCalledWith([{ id: 'skeleton' }]);
    expect(scene).toEqual({
      elements: [{ id: 'converted', type: 'rectangle', x: 10, y: 20 }],
      appState: {},
      files: { image: { id: 'image' } },
      scrollToContent: true,
    });
  });

  it('normalizes a valid Excalidraw scene without conversion', async () => {
    const payload = {
      elements: [{ id: 'one', type: 'text', text: 'hello' }],
      appState: { viewBackgroundColor: '#fff' },
      files: {},
    };
    await expect(preparePageDiagramScene(payload)).resolves.toEqual({
      ...payload,
      scrollToContent: true,
    });
  });

  it.each([
    ['malformed JSON', '{'],
    ['missing Mermaid source', { type: 'mermaid' }],
    ['missing elements', { appState: {} }],
    ['invalid appState', { elements: [], appState: [] }],
    ['invalid files', { elements: [], files: [] }],
  ])('rejects %s non-destructively', async (_name, payload) => {
    expect(() => decodePageDiagramPayload(payload)).toThrow(PageDiagramSceneError);
  });
});

describe('pageDiagramSceneFingerprint', () => {
  const baseline = {
    elements: [
      {
        id: 'one',
        type: 'text',
        x: 10,
        y: 20,
        width: 100,
        height: 30,
        text: 'hello',
        strokeColor: '#111',
        backgroundColor: '#fff',
      },
    ],
    appState: { viewBackgroundColor: '#eee', currentItemStrokeColor: '#f00' },
    files: { file1: { id: 'file1', dataURL: 'data:image/png;base64,one' } },
  };

  it.each([
    ['text', (scene) => (scene.elements[0].text = 'changed')],
    ['position', (scene) => (scene.elements[0].x = 11)],
    ['size', (scene) => (scene.elements[0].width = 101)],
    ['style', (scene) => (scene.elements[0].strokeColor = '#222')],
    ['file content', (scene) => (scene.files.file1.dataURL = 'data:image/png;base64,two')],
    ['file count', (scene) => (scene.files.file2 = { id: 'file2' })],
    ['element count', (scene) => scene.elements.push({ id: 'two', type: 'rectangle' })],
    ['canvas background', (scene) => (scene.appState.viewBackgroundColor = '#000')],
  ])('changes when %s changes', (_name, mutate) => {
    const changed = structuredClone(baseline);
    mutate(changed);
    expect(pageDiagramSceneFingerprint(changed)).not.toBe(pageDiagramSceneFingerprint(baseline));
  });

  it('ignores transient current-tool app state and object key order', () => {
    const normalized = structuredClone(baseline);
    normalized.appState.currentItemStrokeColor = '#0f0';
    normalized.files = {
      file1: { dataURL: 'data:image/png;base64,one', id: 'file1' },
    };
    expect(pageDiagramSceneFingerprint(normalized)).toBe(pageDiagramSceneFingerprint(baseline));
  });
});
