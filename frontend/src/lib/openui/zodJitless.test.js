import { describe, expect, it, vi } from 'vitest';

// Zod's JIT compiler probes `new Function` when it defines an object schema.
// The app's CSP has no 'unsafe-eval', so the browser reports that probe as a
// securitypolicyviolation even though Zod swallows the thrown error. Loading
// the widget library must not touch the Function constructor at all.
const program = 'root = Card([Title("Open work")])';

describe('OpenUI widget parsing under a strict CSP', () => {
  it('parses a program without invoking the Function constructor', async () => {
    const functionSpy = vi.spyOn(globalThis, 'Function');

    try {
      vi.resetModules();
      const { widgetLibrary } = await import('./library.js');
      const { createParser } = await import('@openuidev/svelte-lang');

      const parser = createParser(widgetLibrary.toJSONSchema());
      const result = parser.parse(program);

      expect(result.meta?.errors ?? []).toEqual([]);
      expect(functionSpy).not.toHaveBeenCalled();
    } finally {
      functionSpy.mockRestore();
    }
  });
});
