import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test, type Page } from "../fixtures/context-path";
import { generateIteration, generateWorkspace } from "../fixtures/test-data";
import { BoardPage } from "../pages/board.page";
import { IterationPage } from "../pages/iteration.page";

// Client-side navigation is the point: a full page reload rebuilds the
// workspace store, which is what masked this bug in earlier coverage.
async function clickWorkspaceNav(page: Page, testId: string) {
	await page.getByTestId(testId).click();
}

/**
 * Boards, backlogs, and pickers read iterations from the shared workspace
 * store, which caches the bootstrap directory. Creating an iteration on the
 * iterations page used to leave that cache untouched, so the new iteration was
 * missing from the board's swimlane/filter choices until the next background
 * refresh.
 */
test.describe("Iteration reference invalidation", () => {
	test("a workspace iteration created on the iterations page becomes available on the board", async ({
		page,
		request,
	}) => {
		const workspace = generateWorkspace();
		const created = await createWorkspaceViaAPI(request, {
			name: workspace.name,
			key: workspace.key,
			description: workspace.description ?? "iteration reference invalidation",
		});
		const workspaceId = created.id as number;

		// Prime the shared workspace store from a board that has no iterations.
		const boardPage = new BoardPage(page);
		await boardPage.goto(String(workspaceId));
		await boardPage.verifyBoardVisible();
		await expect(page.getByTestId("board-iteration-filter")).toHaveCount(0);

		// Reach the iterations page through the SPA, not a full page load.
		await clickWorkspaceNav(page, "workspace-nav-iterations");
		const iterationPage = new IterationPage(page);
		const iteration = generateIteration("invalidate");
		await iterationPage.createIteration({
			name: iteration.name,
			start_date: iteration.start_date,
			end_date: iteration.end_date,
			status: "active",
		});

		// Return to the board the same way and assert the new iteration is live.
		await clickWorkspaceNav(page, "workspace-nav-board");
		await boardPage.verifyBoardVisible();
		await expect(page.getByTestId("board-iteration-filter")).toBeVisible();
		await expect(
			page.getByTestId("board-iteration-filter").getByText(iteration.name),
		).toBeVisible();
	});
});
