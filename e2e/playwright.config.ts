import { defineConfig, devices } from "@playwright/test";

// Per-run path overrides let `run-e2e.sh` isolate concurrent invocations.
// Defaults preserve the old behavior for ad-hoc `bun --bun playwright test` runs.
const AUTH_FILE = process.env.E2E_AUTH_FILE ?? ".auth/user.json";
const OUTPUT_DIR = process.env.E2E_OUTPUT_DIR ?? "test-results";
const REPORT_DIR = process.env.E2E_REPORT_DIR ?? "playwright-report";
const SUMMARY_PATH = process.env.E2E_SUMMARY_PATH ?? "e2e-summary.json";
const FRESH_SETUP_PHASE = process.env.E2E_FRESH_SETUP_PHASE ?? "";
const E2E_BROWSER = process.env.E2E_BROWSER ?? "chromium";
const CRITICAL_BROWSER_MATRIX = process.env.E2E_CRITICAL_BROWSER_MATRIX === "1";
const LIVE_JIRA_IMPORT = process.env.E2E_LIVE_JIRA_IMPORT === "1";
const CHROMIUM_EXECUTABLE_PATH =
	process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
const LAUNCH_OPTIONS = CHROMIUM_EXECUTABLE_PATH
	? { executablePath: CHROMIUM_EXECUTABLE_PATH }
	: undefined;
const VIDEO_MODE = CHROMIUM_EXECUTABLE_PATH ? "off" : "retain-on-failure";

const browserDevices = {
	chromium: devices["Desktop Chrome"],
	firefox: devices["Desktop Firefox"],
	webkit: devices["Desktop Safari"],
} as const;

type MatrixBrowser = keyof typeof browserDevices;

function matrixBrowser(): MatrixBrowser {
	if (E2E_BROWSER in browserDevices) return E2E_BROWSER as MatrixBrowser;
	throw new Error(
		`E2E_BROWSER must be chromium, firefox, or webkit; got ${E2E_BROWSER}`,
	);
}

function browserUse(browser: MatrixBrowser, authenticated = true) {
	return {
		...browserDevices[browser],
		reducedMotion: "reduce" as const,
		storageState: authenticated ? AUTH_FILE : { cookies: [], origins: [] },
		...(browser === "chromium" && LAUNCH_OPTIONS
			? { launchOptions: LAUNCH_OPTIONS }
			: {}),
	};
}

const selectedBrowser = matrixBrowser();
const criticalGrep = CRITICAL_BROWSER_MATRIX ? /@critical-browser/ : /$a/;
const globalSetupMatch = /.*\/global\.setup\.ts/;
const noTests = /$a/;
const freshSetupTests = /.*\/fresh-setup(?:-restart)?\.spec\.ts/;
const liveJiraImportTests = /.*\/jira-import(?:-xray)?-live\.spec\.ts/;
// Wire-mail specs need the WIRE_MAILLOOP=1 runner (local Stalwart + TLS
// skip-verify); they are excluded from every other run, including CI.
const wireMailLoopTests = /.*\/email-portal-wire-loop\.spec\.ts/;
const wireMailLoop = (process.env.WIRE_MAILLOOP ?? "") === "1";
const standardTestIgnore = LIVE_JIRA_IMPORT
	? [freshSetupTests, /.*\/website-api-docs\.spec\.ts/, ...(wireMailLoop ? [] : [wireMailLoopTests])]
	: [
			freshSetupTests,
			liveJiraImportTests,
			/.*\/website-api-docs\.spec\.ts/,
			...(wireMailLoop ? [] : [wireMailLoopTests]),
		];

/**
 * Playwright configuration for Windshift E2E tests
 * See https://playwright.dev/docs/test-configuration
 */
export default defineConfig({
	testDir: "./tests",
	outputDir: OUTPUT_DIR,

	/* Run tests in files in parallel */
	fullyParallel: true,

	/* Fail the build on CI if you accidentally left test.only in the source code */
	forbidOnly: !!process.env.CI,

	/* Fail once and retain the evidence instead of spending CI time on retries. */
	retries: 0,

	/* WebKit throttles animation frames for background pages on Linux, which can
	 * prevent actionability checks from observing a stable element. Keep its CI
	 * matrix job to one page; other browsers retain parallel coverage. */
	workers: process.env.CI ? (selectedBrowser === "webkit" ? 1 : 2) : 4,

	/* Reporter to use */
	reporter: [
		["html", { outputFolder: REPORT_DIR }],
		["list"],
		[
			"./helpers/result-policy-reporter.mjs",
			{
				outputFile: SUMMARY_PATH,
				githubSummaryFile: process.env.GITHUB_STEP_SUMMARY,
				failOnUnexpectedSkip: process.env.E2E_FAIL_ON_UNEXPECTED_SKIP === "1",
				failOnRetry: process.env.E2E_FAIL_ON_RETRY === "1",
			},
		],
		...(process.env.E2E_SCREENSHOTS === "1"
			? [["./helpers/screenshot-reporter.ts"]]
			: []),
		...(process.env.CI ? [["github"]] : []),
	],

	/* Shared settings for all the projects below */
	use: {
		/* Base URL to use in actions like `await page.goto('/')` */
		baseURL: process.env.BASE_URL || "http://localhost:8080",

		/* Keep the first failure trace; retries are disabled. */
		trace: "retain-on-failure",

		/* Capture an end-of-test screenshot when the documentation flag is set;
		 * otherwise only on failure. The screenshot-reporter (registered above)
		 * copies the resulting PNGs into e2e/screenshots/<spec>/<test>.png. */
		screenshot: process.env.E2E_SCREENSHOTS === "1" ? "on" : "only-on-failure",

		/* Video on failure */
		video: VIDEO_MODE,

		/* Maximum time each action can take */
		actionTimeout: 10000,
	},

	/* Global timeout for each test */
	timeout: 60000,

	/* Timeout for expect assertions */
	expect: {
		timeout: 10000,
	},

	/* Configure projects for major browsers */
	projects: [
		// The broad suite remains Chromium-only. Critical projects have a
		// matching setup dependency so Firefox/WebKit do not need Chromium
		// installed merely to create their authenticated storage state.
		{
			name: "setup-chromium",
			testMatch:
				!CRITICAL_BROWSER_MATRIX || selectedBrowser === "chromium"
					? globalSetupMatch
					: noTests,
			testDir: ".",
			use: browserUse("chromium", false),
		},
		{
			name: "setup-firefox",
			testMatch:
				CRITICAL_BROWSER_MATRIX && selectedBrowser === "firefox"
					? globalSetupMatch
					: noTests,
			testDir: ".",
			use: browserUse("firefox", false),
		},
		{
			name: "setup-webkit",
			testMatch:
				CRITICAL_BROWSER_MATRIX && selectedBrowser === "webkit"
					? globalSetupMatch
					: noTests,
			testDir: ".",
			use: browserUse("webkit", false),
		},

		{
			name: "chromium",
			testIgnore: standardTestIgnore,
			use: browserUse("chromium"),
			dependencies: ["setup-chromium"],
		},

		{
			name: "chromium-critical",
			grep: criticalGrep,
			testIgnore: standardTestIgnore,
			use: browserUse("chromium"),
			dependencies: ["setup-chromium"],
		},
		{
			name: "firefox-critical",
			grep: criticalGrep,
			testIgnore: standardTestIgnore,
			use: browserUse("firefox"),
			dependencies: ["setup-firefox"],
		},
		{
			name: "webkit-critical",
			grep: criticalGrep,
			testIgnore: standardTestIgnore,
			use: browserUse("webkit"),
			dependencies: ["setup-webkit"],
		},

		{
			name: "fresh-setup",
			testMatch:
				FRESH_SETUP_PHASE === "initial" ? /.*\/fresh-setup\.spec\.ts/ : /$a/,
			retries: 0,
			use: browserUse(selectedBrowser, false),
		},

		{
			name: "fresh-setup-restart",
			testMatch:
				FRESH_SETUP_PHASE === "restart"
					? /.*\/fresh-setup-restart\.spec\.ts/
					: /$a/,
			retries: 0,
			use: browserUse(selectedBrowser, false),
		},
	],

	/* Run your local dev server before starting the tests */
	// webServer: {
	//   command: 'cd ../../core && ./windshift -db e2e-test.db -p 8080',
	//   url: 'http://localhost:8080',
	//   reuseExistingServer: !process.env.CI,
	//   timeout: 120 * 1000,
	//   stdout: 'ignore',
	//   stderr: 'pipe',
	// },
});
