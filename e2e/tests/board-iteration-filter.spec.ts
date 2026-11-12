import {
	createIterationViaAPI,
	createWorkspaceViaAPI,
	listIterationTypesViaAPI,
} from "../fixtures/api-helpers";
import {
	expect,
	test,
	type APIRequestContext,
} from "../fixtures/context-path";
import { generateIteration, generateWorkspace } from "../fixtures/test-data";
import { BoardPage } from "../pages/board.page";

/**
 * The board iteration filter bar is only useful when there is an iteration to
 * filter by or an active filter to clear. A board whose iterations are all
 * completed used to render a lone, already-selected "All Items" tab that did
 * nothing when clicked.
 */
test.describe("Board iteration filter", () => {
	let iterationTypeId: number;

	test.beforeAll(async ({ request }) => {
		const types = await listIterationTypesViaAPI(request);
		if (types.length === 0) {
			throw new Error("No iteration types seeded — backend seed changed?");
		}
		iterationTypeId = types[0].id;
	});

	async function createWorkspaceWithIteration(
		request: APIRequestContext,
		status: "planned" | "completed",
	) {
		const workspace = generateWorkspace();
		const created = await createWorkspaceViaAPI(request, {
			name: workspace.name,
			key: workspace.key,
			description: workspace.description ?? "board iteration filter",
		});
		const iteration = generateIteration(status);
		await createIterationViaAPI(request, {
			...iteration,
			workspace_id: created.id,
			type_id: iterationTypeId,
			status,
		});
		return created.id as number;
	}

	test("shows the filter bar when a non-completed iteration exists", async ({
		page,
		request,
	}) => {
		const workspaceId = await createWorkspaceWithIteration(request, "planned");

		const boardPage = new BoardPage(page);
		await boardPage.goto(String(workspaceId));
		await boardPage.verifyBoardVisible();

		await expect(page.getByTestId("board-iteration-filter")).toBeVisible();
		await expect(
			page.getByTestId("board-iteration-filter-all"),
		).toBeVisible();
	});

	test("hides the filter bar when every iteration is completed", async ({
		page,
		request,
	}) => {
		// The personal workspace excludes global iterations, so its filter bar
		// reflects only its own completed iteration. A fresh workspace would be
		// non-deterministic here: global iterations are shared and other specs
		// create them in parallel.
		const personalResponse = await request.get("/api/workspaces/personal", {
			headers: { "Sec-Fetch-Site": "same-origin" },
		});
		expect(
			personalResponse.ok(),
			`load personal workspace failed (${personalResponse.status()})`,
		).toBeTruthy();
		const personal = await personalResponse.json();
		const iteration = generateIteration("completed");
		await createIterationViaAPI(request, {
			...iteration,
			workspace_id: personal.id,
			type_id: iterationTypeId,
			status: "completed",
		});

		const boardPage = new BoardPage(page);
		await boardPage.goto(String(personal.id));
		await boardPage.verifyBoardVisible();

		await expect(page.getByTestId("board-iteration-filter")).toHaveCount(0);
	});
});
