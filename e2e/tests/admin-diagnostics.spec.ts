import { expect, test } from "../fixtures/context-path";

/**
 * Admin diagnostics (/admin/diagnostics). Each subtab owns an API fetch and a
 * section component; a regression in either leaves the section missing or the
 * error state shown. This smoke walks every subtab.
 */

const SECTIONS = [
	{ subtab: "clock", testId: "diagnostics-server-clock" },
	{ subtab: "actions", testId: "diagnostics-action-logs" },
	{ subtab: "webhooks", testId: "diagnostics-webhook-deliveries" },
	{ subtab: "schedulers", testId: "diagnostics-scheduler-runs" },
	{ subtab: "recurrence-volume", testId: "diagnostics-recurrence-volume" },
	{ subtab: "domain-events", testId: "diagnostics-domain-events" },
	{ subtab: "frac-index", testId: "diagnostics-frac-index" },
	{ subtab: "llm-health", testId: "diagnostics-llm-health" },
	{ subtab: "runner-pools", testId: "diagnostics-runner-pools" },
	{ subtab: "database-pools", testId: "diagnostics-database-pools" },
	{ subtab: "cache-memory", testId: "diagnostics-cache-memory" },
	{ subtab: "scm-health", testId: "diagnostics-scm-health" },
] as const;

test.describe("Admin diagnostics", () => {
	for (const section of SECTIONS) {
		test(`${section.subtab} section renders live data or an empty state`, async ({
			page,
		}) => {
			await page.goto(`/admin/diagnostics?subtab=${section.subtab}`);
			await expect(page.getByTestId("diagnostics-page")).toBeVisible();
			const region = page.getByTestId(section.testId);
			await expect(region).toBeVisible({ timeout: 10_000 });
			// A failed backing fetch renders the shared error card.
			await expect(region.getByTestId("diagnostics-section-error")).toHaveCount(0);
		});
	}

	test("llm health renders daily briefing generation coverage", async ({ page }) => {
		await page.goto("/admin/diagnostics?subtab=llm-health");
		await expect(page.getByTestId("diagnostics-llm-health")).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId("briefing-health")).toBeVisible();
		await expect(page.getByTestId("briefing-coverage")).toBeVisible();
		await expect(page.getByTestId("diagnostics-llm-health").getByTestId("diagnostics-section-error")).toHaveCount(0);
	});
});
