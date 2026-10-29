import { expect, test } from "../fixtures/context-path";
import { seedReadmeDemoOnce, type ReadmeDemoState } from "../helpers/readme-demo-seed";
import { shot } from "../helpers/screenshot";

/**
 * README screenshot captures.
 *
 * Seeds a rich demo dataset through the API (helpers/readme-demo-seed.ts),
 * then visits the surfaces the README showcases and saves named captures via
 * the shared `shot()` helper. Opt-in only so the demo seeding and full-page
 * navigation stay out of the regular browser suite; run with:
 *
 *   README_SCREENSHOTS=1 E2E_SCREENSHOTS=1 \
 *     ../core-tests/run-e2e.sh tests/readme-screenshots.spec.ts
 *
 * PNGs land in e2e/screenshots/readme-screenshots/. The app follows the OS
 * color scheme by default, so Playwright's colorScheme emulation produces the
 * light/dark variants; viewport is 1440x900 @2x to match the existing heroes.
 *
 * Views hydrate asynchronously, so each capture waits for the specific API
 * response that populates the visible content before shooting.
 */

const enabled = process.env.README_SCREENSHOTS === "1";
const THEMES = ["light", "dark"] as const;

test.skip(!enabled, "Set README_SCREENSHOTS=1 to regenerate README screenshots.");

test.use({
	viewport: { width: 1440, height: 900 },
	deviceScaleFactor: 2,
});

test.describe("README screenshots", () => {
	let state: ReadmeDemoState;

	test.beforeAll(async ({ request }) => {
		state = await seedReadmeDemoOnce(request);
	});

	for (const theme of THEMES) {
		test.describe(`${theme} theme`, () => {
			test.use({ colorScheme: theme });

			test("backlog", async ({ page }) => {
				const backlogLoaded = page.waitForResponse((r) =>
					r.url().includes("/backlog") || (r.url().includes("/items") && r.request().method() === "GET"),
				);
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/backlog`);
				await backlogLoaded;
				await expect(page.getByTestId("backlog-count-summary")).toBeVisible();
				await shot(page, `backlog-${theme}`);
			});

			test("hierarchy tree", async ({ page }) => {
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/tree`);
				await expect(page.getByTestId(/^tree-row-\d+$/).first()).toBeVisible();
				await shot(page, `tree-${theme}`);
			});

			test("roadmap", async ({ page }) => {
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/roadmap`);
				await expect(page.getByTestId("roadmap-settings-button")).toBeVisible();
				await page.waitForLoadState("networkidle", { timeout: 2000 }).catch(() => {});
				await shot(page, `roadmap-${theme}`);
			});

			test("analytics", async ({ page }) => {
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/analytics`);
				await expect(page.getByTestId("analytics-throughput-header")).toBeVisible();
				await page.waitForLoadState("networkidle", { timeout: 2000 }).catch(() => {});
				await shot(page, `analytics-${theme}`);
			});

			test("milestone detail", async ({ page }) => {
				const progressLoaded = page.waitForResponse((r) =>
					r.url().includes(`/milestones/${state.milestoneId}`) && r.request().method() === "GET",
				);
				await page.goto(`/milestones/${state.milestoneId}`);
				await progressLoaded;
				await page.waitForLoadState("networkidle", { timeout: 2000 }).catch(() => {});
				await shot(page, `milestone-${theme}`);
			});

			test("knowledge page", async ({ page }) => {
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/pages/${state.pageId}`);
				await expect(page.getByTestId("page-canvas")).toBeVisible();
				await page.waitForLoadState("networkidle", { timeout: 2000 }).catch(() => {});
				await shot(page, `pages-${theme}`);
			});

			test("test run", async ({ page }) => {
				const resultsLoaded = page.waitForResponse((r) =>
					/test-runs\/\d+\/detail/.test(r.url()) && r.request().method() === "GET",
				);
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/tests/runs/${state.testRunId}`);
				await resultsLoaded;
				await expect(page.getByTestId("test-run-detail")).toBeVisible();
				await shot(page, `test-run-${theme}`);
			});

			test("test reports", async ({ page }) => {
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/tests/reports`);
				await expect(page.getByTestId("test-reports")).toBeVisible();
				await page.waitForLoadState("networkidle", { timeout: 2000 }).catch(() => {});
				await shot(page, `test-reports-${theme}`);
			});

			test("work item detail", async ({ page }) => {
				const summaryLoaded = page.waitForResponse((r) =>
					/detail-summary/.test(r.url()) && r.request().method() === "GET",
				);
				await page.goto(`/workspaces/${state.cloudWorkspaceId}/items/${state.featuredItemId}`);
				await summaryLoaded;
				await expect(page.getByTestId("item-detail")).toBeVisible();
				await shot(page, `item-detail-${theme}`);
			});
		});
	}
});
