import { describe, expect, test, vi } from 'vitest';

import {
  collectExcalidrawFontAssets,
  EXCALIDRAW_ASSET_ROUTE,
  excalidrawAssetsPlugin,
} from '../../../scripts/excalidraw-assets.js';

const comicShannsFont =
  'excalidraw-assets/fonts/ComicShanns/ComicShanns-Regular-fcb0fc02dcbee4c9846b3e2508668039.woff2';

describe('Excalidraw assets', () => {
  test('emits non-CJK fonts at the self-hosted asset path', () => {
    const emitted = [];
    const plugin = excalidrawAssetsPlugin();

    plugin.configResolved({ command: 'build' });
    plugin.buildStart.call({ emitFile: (asset) => emitted.push(asset) });

    expect(EXCALIDRAW_ASSET_ROUTE).toBe('/excalidraw-assets/');
    expect(emitted.some((asset) => asset.fileName === comicShannsFont)).toBe(true);
    expect(
      emitted.some((asset) => asset.fileName.startsWith('excalidraw-assets/fonts/Xiaolai/'))
    ).toBe(false);
    expect(emitted).toHaveLength(collectExcalidrawFontAssets().length);
  });

  test('serves packaged fonts during Vite development', () => {
    let middleware;
    const plugin = excalidrawAssetsPlugin();
    plugin.configureServer({
      middlewares: {
        use: (handler) => {
          middleware = handler;
        },
      },
    });
    const response = {
      setHeader: vi.fn(),
      end: vi.fn(),
    };
    const next = vi.fn();

    middleware?.({ url: `/${comicShannsFont}` }, response, next);

    expect(next).not.toHaveBeenCalled();
    expect(response.setHeader).toHaveBeenCalledWith('Content-Type', 'font/woff2');
    expect(response.end).toHaveBeenCalledOnce();
  });

  test('leaves Xiaolai to the Excalidraw CDN during Vite development', () => {
    let middleware;
    const plugin = excalidrawAssetsPlugin();
    plugin.configureServer({
      middlewares: {
        use: (handler) => {
          middleware = handler;
        },
      },
    });
    const response = {
      setHeader: vi.fn(),
      end: vi.fn(),
    };
    const next = vi.fn();

    middleware?.(
      {
        url: '/excalidraw-assets/fonts/Xiaolai/Xiaolai-Regular-example.woff2',
      },
      response,
      next
    );

    expect(next).toHaveBeenCalledOnce();
    expect(response.end).not.toHaveBeenCalled();
  });
});
