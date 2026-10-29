import { randomUUID } from "node:crypto";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * Closing the detail modal no longer reloads the board. An in-modal edit must
 * therefore reach the card through the workspace SSE stream, not a close-time
 * refetch.
 */
test.describe("collection board live updates via SSE (WI-1624)", () => {
	test("an in-modal title edit reaches the board card without a reload", async ({
		page,
		request,
		allowConsoleError,
	}, testInfo) => {
		allowConsoleError(/\/api\/logbook\//);
		allowConsoleError(/\/api\/items\/\d+\/recurrence/);

		const suffix = `board-sse-${randomUUID().slice(0, 8)}${testInfo.workerIndex}`;
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(suffix),
		);
		const originalTitle = `${suffix} original`;
		const updatedTitle = `${suffix} updated`;
		const item = await createItemViaAPI(request, workspace.id, {
			title: originalTitle,
		});

		// Require the workspace stream to be live before mutating, so a passing
		// assertion cannot be satisfied by the board's initial snapshot.
		const streamReady = page.waitForResponse(
			(response) =>
				response.url().endsWith(`/api/workspaces/${workspace.id}/events`) &&
				response.status() === 200,
			{ timeout: 15_000 },
		);
		await page.goto(`/workspaces/${workspace.id}/board`);
		await streamReady;
		const card = page.getByTestId(`board-item-${item.id}`);
		await expect(card).toContainText(originalTitle);

		await card.click();
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 10_000,
		});
		await page.getByTestId("item-title-edit").click();
		const input = page.getByTestId("item-title-input");
		await input.fill(updatedTitle);
		const saveResponse = page.waitForResponse(
			(response) =>
				response.url().endsWith(`/api/v2/items/${item.id}`) &&
				response.request().method() === "PATCH",
		);
		await input.press("Enter");
		expect((await saveResponse).ok()).toBeTruthy();
		await expect(page.getByTestId("item-title-edit")).toHaveText(updatedTitle);

		// The card updates while the modal is still open, which only the workspace
		// SSE stream can do now that closing no longer reloads the board.
		await expect(card).toContainText(updatedTitle, { timeout: 8000 });

		await page.keyboard.press("Escape");
		await expect(page.getByTestId("item-detail-ready")).toBeHidden();
		await expect(card).toContainText(updatedTitle);
	});
});
