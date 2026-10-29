import { randomUUID } from "node:crypto";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * Deleting an item from the detail modal must reach the list and tree views
 * through the workspace SSE stream, matching the board/backlog/map views.
 * Previously only the board subscribed, so the list and tree kept rendering a
 * deleted row until a manual reload.
 */
for (const view of ["list", "tree"] as const) {
	test.describe(`collection ${view} live deletion via SSE`, () => {
		test(`a modal delete removes the ${view} row without navigating`, async ({
			page,
			request,
			allowConsoleError,
		}, testInfo) => {
			allowConsoleError(/\/api\/logbook\//);
			allowConsoleError(/\/api\/items\/\d+\/recurrence/);

			const suffix = `${view}-sse-${randomUUID().slice(0, 8)}${testInfo.workerIndex}`;
			const workspace = await createWorkspaceViaAPI(
				request,
				generateWorkspace(suffix),
			);
			const item = await createItemViaAPI(request, workspace.id, {
				title: `${suffix} delete me`,
			});

			// Require the workspace stream to be live before deleting, so the
			// disappearance cannot be satisfied by the initial snapshot.
			const streamReady = page.waitForResponse(
				(response) =>
					response.url().endsWith(`/api/workspaces/${workspace.id}/events`) &&
					response.status() === 200,
				{ timeout: 15_000 },
			);
			await page.goto(`/workspaces/${workspace.id}/${view}`);
			await streamReady;
			await expect(page.getByTestId(`${view}-view`)).toBeVisible();

			const row = page.getByTestId(
				view === "list" ? `workspace-item-row-${item.id}` : `tree-row-${item.id}`,
			);
			await expect(row).toBeVisible();

			// Open the detail modal from the key cell and delete the item there.
			await page.getByTestId(`list-cell-key-${item.id}`).click();
			await expect(page.getByTestId("item-detail-ready")).toBeVisible({
				timeout: 10_000,
			});
			await page.getByTestId("item-detail-actions-menu").click();
			await page.getByTestId("item-delete-open").click();
			const deleteDialog = page.getByTestId("delete-item-dialog");
			await expect(deleteDialog).toBeVisible();
			await page.locator("#item-delete-confirm").click();
			await expect(deleteDialog).toBeHidden({ timeout: 10_000 });
			await expect(page.getByTestId("item-detail-ready")).toBeHidden();

			// The view stays mounted and must drop the row via the SSE-driven
			// collection refresh, not a navigation or manual reload.
			expect(new URL(page.url()).pathname).toContain(`/${view}`);
			await expect(row).toBeHidden({ timeout: 10_000 });
		});
	});
}
