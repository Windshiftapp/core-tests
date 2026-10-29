import { createItemViaAPI, createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";

/**
 * WI-1647: a superseded key-route load must not reclaim the view.
 *
 * A key-route load that is aborted by a newer navigation used to resume, read
 * the still-rendered item out of the shared store, and canonicalize the URL
 * back to it. Because the route prop had been rewritten to the stale item, the
 * navigation effect then loaded that item again and aborted the destination.
 *
 * This drives real SPA history navigation (Back/Forward) through the router and
 * item detail, holding the intermediate key-route summaries so the stale
 * continuation runs while the destination is still pending. It asserts the
 * rendered destination content and the URL, not just the address bar.
 */
test("a superseded key-route load cannot redirect back to the previous item", async ({
	page,
	request,
	allowConsoleError,
}, testInfo) => {
	allowConsoleError(/\/api\/logbook\//);
	// The detail view probes for a recurrence rule; a 404 is the normal case.
	allowConsoleError(/\/api\/items\/\d+\/recurrence/);
	// Aborting a superseded detail-summary fetch can surface as a Chromium
	// network error even though the app handles the cancellation.
	allowConsoleError(/net::ERR_ABORTED/);

	const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
	const workspace = await createWorkspaceViaAPI(request, {
		name: `stale-nav-${stamp}`,
		key: `SNV${stamp.toString().slice(-5)}`.toUpperCase(),
		description: "stale item navigation regression",
	});

	const first = await createItemViaAPI(request, workspace.id, {
		title: `First ${stamp}`,
	});
	const second = await createItemViaAPI(request, workspace.id, {
		title: `Second ${stamp}`,
	});
	const third = await createItemViaAPI(request, workspace.id, {
		title: `Third ${stamp}`,
	});

	const keyPath = (item: { workspace_item_number: number }) =>
		`/workspace/${workspace.key}/item/${item.workspace_item_number}`;

	// Hold the intermediate key-route summaries so neither Back nor Forward can
	// finish before the stale continuation runs.
	let releaseSecondSummary!: () => void;
	const secondSummaryHeld = new Promise<void>((resolve) => {
		releaseSecondSummary = resolve;
	});
	let releaseThirdSummary!: () => void;
	const thirdSummaryHeld = new Promise<void>((resolve) => {
		releaseThirdSummary = resolve;
	});
	const keySummaryNumbers: string[] = [];

	await page.route(
		/\/workspaces\/([^/]+)\/items\/(\d+)\/detail-summary/,
		async (route) => {
			const number =
				route.request().url().match(/\/items\/(\d+)\/detail-summary/)?.[1] ?? "";
			keySummaryNumbers.push(number);
			if (number === String(second.workspace_item_number)) {
				await secondSummaryHeld;
			} else if (number === String(third.workspace_item_number)) {
				await thirdSummaryHeld;
			}
			// The superseded request may already be aborted by the time it is
			// released, which makes continue() reject.
			await route.continue().catch(() => {});
		},
	);

	try {
		await page.goto(`/workspaces/${workspace.id}/items/${first.id}`);
		await expect(page.getByTestId("item-title-edit")).toHaveText(
			`First ${stamp}`,
		);

		// Add key-route history entries without publishing a route yet, then
		// walk them with real Back/Forward so the router resolves each route.
		await page.evaluate(
			([secondPath, thirdPath]) => {
				history.pushState({}, "", secondPath);
				history.pushState({}, "", thirdPath);
			},
			[keyPath(second), keyPath(third)],
		);

		await page.evaluate(() => history.back());
		await expect(page).toHaveURL(new RegExp(`${keyPath(second)}$`));
		await expect.poll(() => keySummaryNumbers.length).toBeGreaterThanOrEqual(1);

		await page.evaluate(() => history.forward());
		await expect(page).toHaveURL(new RegExp(`${keyPath(third)}$`));
		await expect.poll(() => keySummaryNumbers.length).toBeGreaterThanOrEqual(2);

		// Release the destination. With the fix the view stays on the third
		// item; the stale second continuation would have redirected to the first.
		releaseThirdSummary();

		await expect(page.getByTestId("item-title-edit")).toHaveText(
			`Third ${stamp}`,
		);
		// The destination still canonicalizes from the key form to the numeric
		// route; the stale second continuation must not redirect to the first.
		await expect(page).toHaveURL(
			new RegExp(`/workspaces/${workspace.id}/items/${third.id}$`),
		);
	} finally {
		releaseSecondSummary();
		releaseThirdSummary();
		await page.unroute(/\/workspaces\/([^/]+)\/items\/(\d+)\/detail-summary/);
	}
});
