import { createItemViaAPI, createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { BoardPage } from "../pages/board.page";

/**
 * Regression: clicking a tab inside the item-detail modal called navigate() to
 * the standalone item route, which unmounted the board and swapped the modal
 * for the full-page view. The modal now keeps the active tab as local state.
 */
test.describe("item-detail modal — tab switching", () => {
	test("keeps the modal open on the board route when switching tabs", async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		const workspace = await createWorkspaceViaAPI(request, {
			name: `modal-tabs-${stamp}`,
			key: `MT${stamp.slice(-6)}`.toUpperCase(),
			description: "item modal tab switching regression",
		});
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Modal tab item ${stamp}`,
		});

		const boardPage = new BoardPage(page);
		await boardPage.goto(String(workspace.id));
		await boardPage.verifyCardExists(item.title);
		await boardPage.clickCard(item.title);

		const dialog = page.getByTestId("item-detail-modal");
		await expect(dialog).toBeVisible({ timeout: 10_000 });
		await expect(dialog).toContainText(item.title);

		await dialog.getByTestId("item-detail-details-tab").click();
		await expect(dialog.getByTestId("item-details-overview")).toBeVisible({
			timeout: 10_000,
		});

		// The modal stayed mounted on the board instead of navigating to the
		// standalone item route.
		await expect(dialog).toBeVisible();
		expect(new URL(page.url()).pathname).toBe(
			`/workspaces/${workspace.id}/board`,
		);
	});
});
