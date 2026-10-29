import { defineConfig } from "@playwright/test";

export default defineConfig({
	testDir: "./tests",
	testMatch: "website-api-docs.spec.ts",
	use: {
		baseURL: "http://127.0.0.1:4199",
		trace: "retain-on-failure",
		screenshot: "only-on-failure",
	},
	outputDir: "/private/tmp/windshift-api-browser-results",
	webServer: {
		command: "bun ../frontend/website/serve.mjs",
		url: "http://127.0.0.1:4199/rest-api",
		reuseExistingServer: false,
	},
});
