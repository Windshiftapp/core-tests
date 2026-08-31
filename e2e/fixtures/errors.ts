import { test as base, expect } from './context-path';

/**
 * Error-capturing fixture.
 *
 * Attaches listeners to `page.on('pageerror')` and `page.on('console')` so any
 * JS exception or `console.error` that occurs during a test causes the test to
 * fail. Silent handler failures (e.g. a button click that throws inside an
 * async handler) are otherwise invisible to Playwright, which was the gap the
 * button audit is designed to close.
 *
 * Usage:
 *   import { test, expect } from '../fixtures/errors';
 *
 * Opt-out / allowlist for a single test:
 *   test('...', async ({ page, allowConsoleError }) => {
 *     allowConsoleError(/known noisy warning/);
 *     ...
 *   });
 */

export interface ErrorFixtures {
  /**
   * Add a regex pattern that suppresses matching console.error / pageerror
   * messages for the current test. Useful for known-noisy third-party warnings.
   */
  allowConsoleError: (pattern: RegExp) => void;
}

interface CapturedError {
  type: 'pageerror' | 'console.error';
  message: string;
  location?: string;
}

export const test = base.extend<ErrorFixtures>({
  allowConsoleError: async ({ browserName: _browserName }, use, testInfo) => {
    const patterns: RegExp[] = (testInfo as any)._allowedErrorPatterns ?? [];
    (testInfo as any)._allowedErrorPatterns = patterns;
    await use((pattern: RegExp) => {
      patterns.push(pattern);
    });
  },

  page: async ({ page }, use, testInfo) => {
    const errors: CapturedError[] = [];

    const isAllowed = (msg: string): boolean => {
      const patterns: RegExp[] = (testInfo as any)._allowedErrorPatterns ?? [];
      return patterns.some((p) => p.test(msg));
    };

    page.on('pageerror', (err) => {
      const message = `${err.name}: ${err.message}\n${err.stack ?? ''}`;
      if (isAllowed(message)) return;
      errors.push({ type: 'pageerror', message });
    });

    page.on('console', (msg) => {
      if (msg.type() !== 'error') return;
      const text = msg.text();
      const loc = msg.location();
      const locStr = loc.url ? `${loc.url}:${loc.lineNumber}:${loc.columnNumber}` : undefined;
      // Chromium's 4xx/5xx resource errors put the URL in `location`, not `text`.
      // Match against both so URL-based allowlist patterns actually fire.
      const combined = locStr ? `${text} ${locStr}` : text;
      if (isAllowed(combined)) return;
      errors.push({ type: 'console.error', message: text, location: locStr });
    });

    await use(page);

    if (errors.length > 0) {
      const summary = errors
        .map((e, i) => {
          const loc = e.location ? ` (${e.location})` : '';
          return `  [${i + 1}] ${e.type}${loc}: ${e.message.split('\n')[0]}`;
        })
        .join('\n');
      throw new Error(`Test captured ${errors.length} browser error(s):\n${summary}`);
    }
  },
});

export { expect };
