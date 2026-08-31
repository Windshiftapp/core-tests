import { defineConfig, devices } from '@playwright/test';

const authFile = process.env.E2E_AUTH_FILE ?? '.auth/user.json';
const outputDir = process.env.E2E_OUTPUT_DIR ?? 'test-results';
const reportDir = process.env.E2E_REPORT_DIR ?? 'playwright-report';
const summaryPath = process.env.E2E_SUMMARY_PATH ?? 'e2e-summary.json';
const chromiumExecutablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
const freshSetupPhase = process.env.E2E_FRESH_SETUP_PHASE ?? '';
const freshSetupTests = /.*\/fresh-setup(?:-restart)?\.spec\.ts/;

export default defineConfig({
  testDir: './tests',
  outputDir,
  fullyParallel: true,
  forbidOnly: true,
  retries: 0,
  workers: process.env.CI ? 2 : 4,
  reporter: [
    ['html', { outputFolder: reportDir }],
    ['list'],
    [
      './helpers/result-policy-reporter.mjs',
      {
        outputFile: summaryPath,
        githubSummaryFile: process.env.GITHUB_STEP_SUMMARY,
        failOnUnexpectedSkip: true,
        failOnRetry: true,
      },
    ],
    ...(process.env.CI ? [['github']] : []),
  ],
  use: {
    ...devices['Desktop Chrome'],
    baseURL: process.env.BASE_URL || 'http://localhost:8080',
    storageState: authFile,
    reducedMotion: 'reduce',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: chromiumExecutablePath ? 'off' : 'retain-on-failure',
    actionTimeout: 10_000,
    ...(chromiumExecutablePath
      ? { launchOptions: { executablePath: chromiumExecutablePath } }
      : {}),
  },
  timeout: 60_000,
  expect: { timeout: 10_000 },
  projects: [
    {
      name: 'setup',
      testMatch: /.*\/global\.setup\.ts/,
      testDir: '.',
      use: { storageState: { cookies: [], origins: [] } },
    },
    {
      name: 'chromium',
      dependencies: ['setup'],
      testIgnore: freshSetupTests,
    },
    {
      name: 'fresh-setup',
      testMatch: freshSetupPhase === 'initial' ? /.*\/fresh-setup\.spec\.ts/ : /$a/,
      use: { storageState: { cookies: [], origins: [] } },
    },
    {
      name: 'fresh-setup-restart',
      testMatch: freshSetupPhase === 'restart' ? /.*\/fresh-setup-restart\.spec\.ts/ : /$a/,
      use: { storageState: { cookies: [], origins: [] } },
    },
  ],
});
