import { randomUUID } from "node:crypto";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * Like the map, the roadmap is a passive view: without an event stream a
 * server-side deletion only disappears after a manual reload. This proves its
 * workspace SSE subscription (WI-1624) drops the deleted row in place.
 */
test.describe("collection roadmap live updates via SSE (WI-1624)", () => {
	test("a deletion pushed from elsewhere removes the roadmap row without reloading", async ({
		page,
		request,
		allowConsoleError,
	}, testInfo) => {
		allowConsoleError(/\/api\/logbook\//);

		const suffix = `roadmap-sse-${randomUUID().slice(0, 8)}${testInfo.workerIndex}`;
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(suffix),
		);
		const item = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} item`,
		});

		// Require the workspace stream to be live before mutating, so a passing
		// assertion cannot be satisfied by the roadmap's initial snapshot.
		const streamReady = page.waitForResponse(
			(response) =>
				response.url().endsWith(`/api/workspaces/${workspace.id}/events`) &&
				response.status() === 200,
			{ timeout: 15_000 },
		);
		await page.goto(`/workspaces/${workspace.id}/roadmap`);
		await streamReady;
		await expect(page.getByTestId("roadmap-view")).toBeVisible();
		const row = page.getByTestId(`roadmap-item-${item.id}`);
		await expect(row).toBeVisible();

		// Mutate server-side. Publish fires after commit → SSE hub → the roadmap's
		// workspace stream → refreshCollectionDeltas, with no reload or poll.
		const response = await request.delete(`/api/v2/items/${item.id}`);
		expect(response.ok()).toBeTruthy();

		await expect(row).toBeHidden({ timeout: 8000 });
	});
});
