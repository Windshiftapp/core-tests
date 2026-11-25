import { preprocessMeltUI } from '@melt-ui/pp';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';
import { lucideDirectImports } from './lucide-direct-imports.js';

export default defineConfig({
  plugins: [
    lucideDirectImports(),
    svelte({
      preprocess: [preprocessMeltUI()],
      hot: false, // Disable HMR in tests
    }),
  ],
  test: {
    globals: true,
    environment: 'jsdom',
    // `scripts/` holds tests for the guard scripts that `npm run check` runs.
    include: ['src/**/*.{test,spec}.{js,ts}', 'scripts/**/*.{test,spec}.{js,ts}'],
    setupFiles: ['./src/setupTests.js'],
    // Prevent CSS import errors in tests
    css: false,
    // Reporter configuration
    reporters: ['verbose'],
    // Coverage configuration
    coverage: {
      // Keep the installed Istanbul provider and existing coverage baselines.
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
