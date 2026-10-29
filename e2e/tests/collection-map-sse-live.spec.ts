import { randomUUID } from "node:crypto";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
	listItemTypesViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";
import { generateWorkspace } from "../fixtures/test-data";

type ItemType = { id: number; hierarchy_level: number };

/**
 * The collection map is a passive view: without an event stream it only reflects
 * a server-side change after a manual reload. This proves the map's workspace
 * SSE subscription (WI-1624) patches it in place when an item is deleted from
 * another client, well before any reload.
 */
test.describe("collection map live updates via SSE (WI-1624)", () => {
	test("a deletion pushed from elsewhere removes the map card without reloading", async ({
		page,
		request,
		allowConsoleError,
	}, testInfo) => {
		allowConsoleError(/\/api\/logbook\//);

		const suffix = `map-sse-${randomUUID().slice(0, 8)}${testInfo.workerIndex}`;
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(suffix),
		);
		const itemTypes = (await listItemTypesViaAPI(request)) as ItemType[];
		const parentType = itemTypes.find(
			(candidate) =>
				candidate.hierarchy_level >= 0 &&
				itemTypes.some(
					(child) =>
						child.hierarchy_level === -1 ||
						child.hierarchy_level === candidate.hierarchy_level + 1,
				),
		);
		if (!parentType) {
			throw new Error("No item type accepts children");
		}

		const parent = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} parent`,
			item_type_id: parentType.id,
		});

		// Require the workspace stream to be live before mutating, so a passing
		// assertion cannot be satisfied by the map's initial snapshot.
		const streamReady = page.waitForResponse(
			(response) =>
				response.url().endsWith(`/api/workspaces/${workspace.id}/events`) &&
				response.status() === 200,
			{ timeout: 15_000 },
		);
		await page.goto(`/workspaces/${workspace.id}/map`);
		await streamReady;
		await expect(page.getByTestId("map-view")).toBeVisible();
		const card = page.getByTestId(`map-backbone-item-${parent.id}`);
		await expect(card).toBeVisible();

		// Mutate server-side. Publish fires after commit → SSE hub → the map's
		// workspace stream → refreshCollectionDeltas, with no reload or poll.
		const response = await request.delete(`/api/v2/items/${parent.id}`);
		expect(response.ok()).toBeTruthy();

		await expect(card).toBeHidden({ timeout: 8000 });
	});
});
