import { afterEach, describe, expect, test } from 'vitest';

import {
  configureExcalidrawAssets,
  DEFAULT_EXCALIDRAW_CANVAS_COLOR,
  prepareExcalidrawInitialData,
} from './excalidrawSetup.js';

afterEach(() => {
  delete window.__WINDSHIFT_CONTEXT_PATH__;
  delete window.EXCALIDRAW_ASSET_PATH;
});

describe('Excalidraw setup', () => {
  test('uses a same-origin asset path under the configured context path', () => {
    window.__WINDSHIFT_CONTEXT_PATH__ = '/windshift';

    expect(configureExcalidrawAssets()).toBe(
      `${window.location.origin}/windshift/excalidraw-assets/`
    );
    expect(window.EXCALIDRAW_ASSET_PATH).toBe(
      `${window.location.origin}/windshift/excalidraw-assets/`
    );
  });

  test('uses a visible default canvas without mutating the input scene', () => {
    const initialData = {
      elements: [{ id: 'shape-1' }],
      appState: { viewBackgroundColor: '#1e1e1e' },
      files: { image: {} },
    };

    const prepared = prepareExcalidrawInitialData(initialData);

    expect(prepared.appState.viewBackgroundColor).toBe(DEFAULT_EXCALIDRAW_CANVAS_COLOR);
    expect(initialData.appState.viewBackgroundColor).toBe('#1e1e1e');
    expect(prepared.elements).toBe(initialData.elements);
    expect(prepared.files).toBe(initialData.files);
  });

  test('preserves an explicitly selected non-legacy canvas color', () => {
    const prepared = prepareExcalidrawInitialData({
      appState: { viewBackgroundColor: '#fff4cc' },
    });

    expect(prepared.appState.viewBackgroundColor).toBe('#fff4cc');
  });
});
