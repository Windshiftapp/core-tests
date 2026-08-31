import type { Browser, BrowserContext, Page, Response } from '@playwright/test';
import { test as base, expect, mergeTests, request } from '@playwright/test';

const contextPath = normalizeContextPath(process.env.E2E_CONTEXT_PATH || '');
const baseURL = process.env.BASE_URL || 'http://localhost:8080';
const baseOrigin = process.env.BASE_ORIGIN || new URL(baseURL).origin;

function normalizeContextPath(raw: string): string {
  const value = raw.trim();
  if (!value || value === '/') return '';
  return value.startsWith('/') ? value.replace(/\/$/, '') : `/${value.replace(/\/$/, '')}`;
}

function isSameOriginURL(value: string): boolean {
  try {
    return new URL(value, baseURL).origin === baseOrigin;
  } catch {
    return false;
  }
}

export function externalPath(pathOrURL: string): string {
  if (!contextPath || !pathOrURL) return pathOrURL;
  if (pathOrURL.startsWith('//')) return pathOrURL;
  if (pathOrURL.startsWith('/')) {
    if (pathOrURL === contextPath || pathOrURL.startsWith(`${contextPath}/`)) return pathOrURL;
    return `${contextPath}${pathOrURL}`;
  }
  try {
    const url = new URL(pathOrURL, baseURL);
    if (
      url.origin !== baseOrigin ||
      url.pathname === contextPath ||
      url.pathname.startsWith(`${contextPath}/`)
    ) {
      return pathOrURL;
    }
    url.pathname = `${contextPath}${url.pathname}`;
    return url.toString();
  } catch {
    return pathOrURL;
  }
}

export function logicalPath(pathOrURL: string): string {
  if (!contextPath || !pathOrURL) return pathOrURL;
  try {
    const url = pathOrURL.startsWith('/') ? new URL(pathOrURL, baseURL) : new URL(pathOrURL);
    if (url.origin !== baseOrigin) return pathOrURL;
    if (url.pathname === contextPath) return '/';
    if (url.pathname.startsWith(`${contextPath}/`)) {
      return `${url.pathname.slice(contextPath.length)}${url.search}${url.hash}`;
    }
    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    if (pathOrURL === contextPath) return '/';
    if (pathOrURL.startsWith(`${contextPath}/`)) return pathOrURL.slice(contextPath.length) || '/';
    return pathOrURL;
  }
}

function patchPage(page: Page): Page {
  if (!contextPath || (page as any).__windshiftContextPathPatched) return page;
  (page as any).__windshiftContextPathPatched = true;
  const originalGoto = page.goto.bind(page);
  page.goto = ((url: string, options?: Parameters<Page['goto']>[1]): Promise<Response | null> => {
    return originalGoto(externalPath(url), options);
  }) as Page['goto'];
  return page;
}

function patchContext(context: BrowserContext): BrowserContext {
  if (!contextPath || (context as any).__windshiftContextPathPatched) return context;
  (context as any).__windshiftContextPathPatched = true;

  const originalNewPage = context.newPage.bind(context);
  context.newPage = (async (...args: Parameters<BrowserContext['newPage']>) => {
    const page = await originalNewPage(...args);
    return patchPage(page);
  }) as BrowserContext['newPage'];

  for (const page of context.pages()) patchPage(page);
  context.on('page', patchPage);
  return context;
}

// APIRequestContext instances (the worker `request` fixture, page.request,
// context.request, and anything from playwright.request.newContext()) all share
// one prototype. Root-absolute request URLs like `/api/items` ignore the path
// portion of baseURL, so under a context path they'd hit the unprefixed route
// and 404. Patch the shared prototype once so every request URL is routed
// through externalPath() — the API-call analogue of the page.goto patch above.
const REQUEST_METHODS = ['fetch', 'get', 'post', 'put', 'patch', 'delete', 'head'] as const;

function patchAPIRequestContextPrototype(sample: object): void {
  if (!contextPath) return;
  const proto = Object.getPrototypeOf(sample) as any;
  if (!proto || proto.__windshiftContextPathPatched) return;
  proto.__windshiftContextPathPatched = true;

  for (const method of REQUEST_METHODS) {
    const original = proto[method];
    if (typeof original !== 'function') continue;
    proto[method] = function patched(this: unknown, url: unknown, ...rest: unknown[]) {
      return original.call(this, typeof url === 'string' ? externalPath(url) : url, ...rest);
    };
  }
}

function patchBrowser(browser: Browser): Browser {
  if (!contextPath || (browser as any).__windshiftContextPathPatched) return browser;
  (browser as any).__windshiftContextPathPatched = true;

  const originalNewContext = browser.newContext.bind(browser);
  browser.newContext = (async (...args: Parameters<Browser['newContext']>) => {
    const context = await originalNewContext(...args);
    return patchContext(context);
  }) as Browser['newContext'];
  return browser;
}

const unprefixedRoots = [
  '/api',
  '/rest',
  '/mcp',
  '/_app',
  '/remoteEntry.js',
  '/windshift-3.svg',
  '/favicon-32x32.png',
  '/apple-touch-icon.png',
  '/forms/widget.js',
  '/workspaces',
  '/personal',
  '/admin',
  '/portal',
  '/forms',
  '/board',
  '/collections',
  '/profile',
  '/notifications',
  '/search',
  '/channels',
  '/milestones',
  '/iterations',
  '/teams',
  '/time',
  '/assets',
  '/api-docs',
  '/oauth',
  '/cli',
  '/set-password',
];

function isUnprefixedWindshiftRequest(urlString: string): boolean {
  if (!contextPath) return false;
  let url: URL;
  try {
    url = new URL(urlString);
  } catch {
    return false;
  }
  if (url.origin !== baseOrigin) return false;
  if (
    url.pathname === '/' ||
    url.pathname === contextPath ||
    url.pathname.startsWith(`${contextPath}/`)
  )
    return false;
  return unprefixedRoots.some(
    (root) => url.pathname === root || url.pathname.startsWith(`${root}/`)
  );
}

export const test = base.extend<{ _contextPathLeakCheck: undefined }, { _patchBrowser: undefined }>(
  {
    _patchBrowser: [
      async ({ browser, playwright }, use) => {
        patchBrowser(browser);
        // Grab a throwaway APIRequestContext to reach the shared prototype and patch
        // it before any test-scoped `request` fixture is constructed.
        const probe = await playwright.request.newContext();
        patchAPIRequestContextPrototype(probe);
        await probe.dispose();
        await use(undefined);
      },
      { scope: 'worker', auto: true },
    ],

    context: async ({ context }, use) => {
      patchContext(context);
      await use(context);
    },

    page: async ({ page }, use) => {
      patchPage(page);
      await use(page);
    },

    _contextPathLeakCheck: [
      async ({ context }, use) => {
        const leaks: string[] = [];
        const onRequest = (request: any) => {
          const url = request.url();
          if (isSameOriginURL(url) && isUnprefixedWindshiftRequest(url)) {
            leaks.push(url);
          }
        };
        context.on('request', onRequest);
        await use(undefined);
        context.off('request', onRequest);
        expect(leaks, 'unprefixed same-origin Windshift requests').toEqual([]);
      },
      { auto: true },
    ],
  }
);

export type {
  APIRequestContext,
  Browser,
  BrowserContext,
  Locator,
  Page,
  Response,
} from '@playwright/test';
export { expect, mergeTests, request };
