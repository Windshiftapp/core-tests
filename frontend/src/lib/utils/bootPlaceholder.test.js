import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { describe, expect, test } from 'vitest';

describe('pre-mount boot placeholder', () => {
  test('uses the branded loader centered in the viewport until Svelte mounts', async () => {
    const [html, main] = await Promise.all([
      readFile(path.resolve('index.html'), 'utf8'),
      readFile(path.resolve('src/main.js'), 'utf8'),
    ]);
    const bootStyles = html.match(/#windshift-boot\s*\{([^}]*)\}/)?.[1] ?? '';

    expect(html).toContain('id="windshift-boot"');
    expect(html).toContain('class="boot-logo" src="windshift-3.svg"');
    expect(html).not.toContain('class="boot-spinner"');
    expect(bootStyles).not.toMatch(/position\s*:\s*(?:fixed|absolute)/);
    expect(bootStyles).toMatch(/display\s*:\s*grid/);
    expect(bootStyles).toMatch(/min-height\s*:\s*100dvh/);
    expect(bootStyles).toMatch(/place-items\s*:\s*center/);
    expect(main.indexOf('target.replaceChildren()')).toBeGreaterThan(-1);
    expect(main.indexOf('target.replaceChildren()')).toBeLessThan(main.indexOf('mount(App'));
  });

  test('dark styling follows data-color-mode, not the OS color scheme', async () => {
    const html = await readFile(path.resolve('index.html'), 'utf8');

    // The app theme is driven by html[data-color-mode]; keying boot styles off
    // prefers-color-scheme renders a dark box in light mode when the OS is dark.
    expect(html).not.toMatch(/@media\s*\(prefers-color-scheme/);
    expect(html).toMatch(/html\[data-color-mode="dark"\]\s+#windshift-boot\s*\{/);
    expect(html).toMatch(/html\[data-color-mode="dark"\]\s+#windshift-boot\s+p\s*\{/);
  });
});
