import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import {
	type Locator,
	expect,
	test,
} from "../fixtures/context-path";
import { generateItem, generateWorkspace } from "../fixtures/test-data";

/**
 * Board multi-select mode.
 *
 * Selection is an explicit mode: cards carry no checkbox at rest, entering the
 * mode dims unselected cards and reveals the bulk toolbar, and leaving the mode
 * restores the plain board. This exercises the whole browser workflow because
 * the translucency and chrome toggling only exist in the rendered board.
 */

test.describe.configure({ retries: 0 });

test.describe("Board multi-select mode", () => {
	test("dims unselected cards and reveals bulk actions only while active", async ({
		page,
		request,
	}) => {
		test.setTimeout(60_000);
		await authenticateAdminRequest(request);

		const suffix = `bms${Date.now()}`;
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const first = await createItemViaAPI(request, ws.id, {
			title: generateItem(ws.id, `${suffix}-a`).title,
		});
		const second = await createItemViaAPI(request, ws.id, {
			title: generateItem(ws.id, `${suffix}-b`).title,
		});

		const opacityOf = (locator: Locator) =>
			locator.evaluate((el) => getComputedStyle(el).opacity);

		try {
			await page.goto(`/workspaces/${ws.id}/board`);
			const firstCard = page.getByTestId(`board-item-${first.id}`);
			const secondCard = page.getByTestId(`board-item-${second.id}`);
			await expect(firstCard).toBeVisible({ timeout: 15_000 });
			await expect(secondCard).toBeVisible();

			// At rest the board shows no selection chrome.
			await expect(page.getByTestId(`board-card-select-${first.id}`)).toHaveCount(0);
			await expect(page.getByTestId("board-bulk-toolbar")).toHaveCount(0);
			await expect.poll(() => opacityOf(firstCard)).toBe("1");

			// Enter selection mode: checkboxes appear, cards dim, toolbar shows.
			await page.getByTestId("board-select-mode-toggle").click();
			await expect(page.getByTestId("board-bulk-toolbar")).toBeVisible();
			await expect(page.getByTestId("board-bulk-hint")).toBeVisible();
			await expect(page.getByTestId(`board-card-select-${first.id}`)).toBeVisible();
			await expect.poll(() => opacityOf(firstCard)).toBe("0.5");
			await expect.poll(() => opacityOf(secondCard)).toBe("0.5");

			// Clicking a card selects it and makes it fully opaque again.
			await firstCard.click();
			await expect(firstCard).toHaveAttribute("data-selected", "true");
			await expect(page.getByTestId("board-bulk-count")).toContainText("1");
			await expect.poll(() => opacityOf(firstCard)).toBe("1");
			await expect.poll(() => opacityOf(secondCard)).toBe("0.5");
			await expect(page.getByTestId("board-bulk-status")).toBeEnabled();

			// Clear empties the selection but keeps the mode open.
			await page.getByTestId("board-bulk-clear").click();
			await expect(firstCard).toHaveAttribute("data-selected", "false");
			await expect(page.getByTestId("board-bulk-hint")).toBeVisible();

			// Done exits the mode and removes the selection chrome entirely.
			await page.getByTestId("board-bulk-done").click();
			await expect(page.getByTestId("board-bulk-toolbar")).toHaveCount(0);
			await expect(page.getByTestId(`board-card-select-${first.id}`)).toHaveCount(0);
			await expect.poll(() => opacityOf(firstCard)).toBe("1");
		} finally {
			expect((await request.delete(`/api/v2/workspaces/${ws.id}`)).status()).toBe(204);
		}
	});
});
