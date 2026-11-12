import {
	createIterationViaAPI,
	createItemViaAPI,
	createWorkspaceViaAPI,
	listIterationTypesViaAPI,
} from "../fixtures/api-helpers";
import {
	expect,
	test,
	type APIRequestContext,
	type Page,
} from "../fixtures/context-path";
import {
	generateItem,
	generateIteration,
	generateMilestone,
	generateWorkspace,
} from "../fixtures/test-data";
import { BoardPage } from "../pages/board.page";
import { ItemPage } from "../pages/item.page";
import { IterationPage } from "../pages/iteration.page";
import { MilestonePage } from "../pages/milestone.page";

// In-app navigation is the point: a full page load rebuilds the shared stores,
// which is exactly what masked the stale-cache bug.
async function clickWorkspaceNav(page: Page, testId: string) {
	await page.getByTestId(testId).click();
}

async function createWorkspace(request: APIRequestContext) {
	const workspace = generateWorkspace();
	const created = await createWorkspaceViaAPI(request, {
		name: workspace.name,
		key: workspace.key,
		description: workspace.description ?? "reference data invalidation",
	});
	return created.id as number;
}

async function findMilestoneId(
	request: APIRequestContext,
	workspaceId: number,
	name: string,
) {
	const response = await request.get(
		`/api/v2/workspaces/${workspaceId}/milestones`,
	);
	const body = await response.json();
	const rows = (body.data ?? body) as Array<{ id: number; name: string }>;
	return rows.find((row) => row.name === name)?.id;
}

test.describe("Reference data invalidation", () => {
	// NOTE: global iterations are shared across every workspace by design, so a
	// workspace board can legitimately keep its filter bar after its own last
	// iteration is deleted. Assertions below target the mutated entry, which is
	// deterministic even while other specs create global iterations in parallel.
	test("a milestone created on the milestones page is offered on an item", async ({
		page,
		request,
	}) => {
		const workspaceId = await createWorkspace(request);
		const itemData = generateItem(0, "ms-invalidate");
		await createItemViaAPI(request, workspaceId, { title: itemData.title });

		// Prime the shared store from a board that has no milestones.
		const boardPage = new BoardPage(page);
		await boardPage.goto(String(workspaceId));
		await boardPage.verifyBoardVisible();

		// Create the milestone through the app, then return to the item.
		await clickWorkspaceNav(page, "workspace-nav-milestones");
		const milestonePage = new MilestonePage(page);
		const milestone = generateMilestone("invalidate");
		await milestonePage.createMilestone({ name: milestone.name });
		const milestoneId = await findMilestoneId(
			request,
			workspaceId,
			milestone.name,
		);
		expect(milestoneId, "milestone was not created").toBeTruthy();

		await clickWorkspaceNav(page, "workspace-nav-backlog");
		const itemPage = new ItemPage(page);
		await itemPage.openItemDetailModal(itemData.title);

		const dialog = page.locator('[role="dialog"]');
		await dialog.locator('[data-testid="milestone-field"]').click();
		await expect(
			page.locator(`[role="option"][data-option-id="${milestoneId}"]`),
		).toBeVisible();
	});

	test("the board stops offering an iteration deleted on the iterations page", async ({
		page,
		request,
	}) => {
		const workspaceId = await createWorkspace(request);
		const types = await listIterationTypesViaAPI(request);
		expect(types.length, "no iteration types seeded").toBeGreaterThan(0);

		const iteration = generateIteration("last");
		await createIterationViaAPI(request, {
			...iteration,
			workspace_id: workspaceId,
			type_id: types[0].id,
			status: "active",
		});

		const boardPage = new BoardPage(page);
		await boardPage.goto(String(workspaceId));
		await boardPage.verifyBoardVisible();
		await expect(page.getByTestId("board-iteration-filter")).toBeVisible();
		await expect(
			page.getByTestId("board-iteration-filter").getByText(iteration.name),
		).toBeVisible();

		// Delete the iteration through the app, then return to the board.
		await clickWorkspaceNav(page, "workspace-nav-iterations");
		const iterationPage = new IterationPage(page);
		await iterationPage.deleteIteration(iteration.name);

		await clickWorkspaceNav(page, "workspace-nav-board");
		await boardPage.verifyBoardVisible();
		await expect(
			page.getByTestId("board-iteration-filter").getByText(iteration.name),
		).toHaveCount(0);
	});
});
