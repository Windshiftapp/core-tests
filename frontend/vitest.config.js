import { readFileSync } from 'node:fs';
import { preprocessMeltUI } from '@melt-ui/pp';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

const manifest = JSON.parse(
  readFileSync(new URL('../suite-manifest.json', import.meta.url), 'utf8')
);
const fastTests = manifest.frontend.fast.map((file) => file.replace(/^frontend\//, ''));

export default defineConfig({
  plugins: [
    svelte({
      preprocess: [preprocessMeltUI()],
    }),
  ],
  test: {
    globals: true,
    environment: 'jsdom',
    include: fastTests,
    setupFiles: ['./src/setupTests.js'],
    // Prevent CSS import errors in tests
    css: false,
    // Reporter configuration
    reporters: ['verbose'],
    // Coverage configuration
    coverage: {
      // Bun 1.4 can emit zero-width V8 ranges that break suite-wide merging.
      provider: 'istanbul',
      reporter: ['text', 'html'],
      exclude: ['node_modules/', 'src/setupTests.js', '**/*.test.{js,ts}', '**/*.spec.{js,ts}'],
    },
    // Svelte 5 requires browser conditions for component tests
    alias: {
      // Ensure Svelte uses client-side code in tests
      svelte: 'svelte',
    },
  },
  resolve: {
    // Ensure browser conditions are used for Svelte 5
    conditions: ['browser', 'development'],
  },
});
