import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test, type Locator, type Page } from "../fixtures/context-path";
import type { APIRequestContext } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * Board milestone swimlanes: grouping, drag-to-assign/clear, the
 * multi-milestone confirmation, and the owner-configured public board.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

async function listWorkspaceStatuses(
	request: APIRequestContext,
	workspaceId: number,
): Promise<Array<{ id: number; name: string }>> {
	const response = await request.get(`/api/v2/workspaces/${workspaceId}/statuses`, {
		headers: SEC_FETCH,
	});
	expect(response.ok(), `list statuses: ${response.status()}`).toBeTruthy();
	const body = await response.json();
	return body.data ?? body;
}

interface Milestone {
	id: number;
	target_date?: string;
	status?: string;
}

async function createMilestone(
	request: Parameters<typeof createItemViaAPI>[0],
	workspaceId: number,
	name: string,
	targetDate: string,
	status = "planning",
): Promise<Milestone> {
	const response = await request.post(`/api/v2/workspaces/${workspaceId}/milestones`, {
		headers: SEC_FETCH,
		data: { name, target_date: targetDate, status },
	});
	expect(response.ok(), `create milestone: ${response.status()} ${await response.text()}`).toBeTruthy();
	return (await response.json()).data;
}

async function createGlobalMilestone(
	request: Parameters<typeof createItemViaAPI>[0],
	name: string,
	targetDate: string,
): Promise<Milestone> {
	const response = await request.post(`/api/v2/milestones`, {
		headers: SEC_FETCH,
		data: { name, target_date: targetDate, status: "planning" },
	});
	expect(
		response.ok(),
		`create global milestone: ${response.status()} ${await response.text()}`,
	).toBeTruthy();
	return (await response.json()).data;
}

async function itemMilestoneIds(
	request: Parameters<typeof createItemViaAPI>[0],
	itemId: number,
): Promise<number[]> {
	const response = await request.get(`/api/v2/items/${itemId}`, { headers: SEC_FETCH });
	expect(response.ok(), `get item: ${response.status()}`).toBeTruthy();
	const body = await response.json();
	return (body.data?.milestones ?? []).map((milestone: Milestone) => milestone.id);
}

async function dragCardTo(page: Page, card: Locator, target: Locator) {
	await card.scrollIntoViewIfNeeded();
	await target.scrollIntoViewIfNeeded();
	const cardBox = await card.boundingBox();
	const targetBox = await target.boundingBox();
	if (!cardBox || !targetBox) throw new Error("Drag source and target must be visible");

	await page.mouse.move(cardBox.x + cardBox.width / 2, cardBox.y + cardBox.height / 2);
	await page.mouse.down();
	await page.mouse.move(cardBox.x + 12, cardBox.y + 12);
	await page.mouse.move(
		targetBox.x + targetBox.width / 2,
		targetBox.y + Math.min(targetBox.height / 2, 120),
		{ steps: 12 },
	);
	await page.mouse.up();
}

function boardCard(page: Page, itemId: number): Locator {
	return page.getByTestId(`board-item-${itemId}`);
}

async function selectMilestoneGrouping(page: Page, firstLaneId: number) {
	await page.getByTestId("board-group-by-menu").click();
	await page.getByTestId("board-group-by-milestone").click();
	await expect(page.getByTestId(`board-lane-milestone-${firstLaneId}`)).toBeVisible();
}

test.describe("board milestone swimlanes", () => {
	test("groups, assigns, clears and confirms multi-milestone drops", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		await page.setViewportSize({ width: 1600, height: 2600 });
		await authenticateAdminRequest(request);

		const suffix = `bms${Date.now()}`;
		const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const statuses = await listWorkspaceStatuses(request, workspace.id);
		expect(statuses.length).toBeGreaterThanOrEqual(2);

		const beta = await createMilestone(request, workspace.id, `${suffix} Beta`, "2026-11-01");
		const ga = await createMilestone(request, workspace.id, `${suffix} GA`, "2026-12-01");
		const later = await createMilestone(request, workspace.id, `${suffix} Later`, "2027-01-01");

		const unassigned = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} unassigned`,
			status_id: statuses[0].id,
		});
		const alpha = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} alpha`,
			status_id: statuses[0].id,
			milestone_ids: [ga.id],
		});
		const multi = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} multi`,
			status_id: statuses[0].id,
			milestone_ids: [ga.id, beta.id],
		});

		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId("board-view")).toBeVisible({ timeout: 15_000 });
		await selectMilestoneGrouping(page, beta.id);

		const lanes = page.getByTestId(/^board-lane-/);
		await expect(lanes).toHaveCount(4);
		await expect(lanes.nth(0)).toHaveAttribute("data-testid", `board-lane-milestone-${beta.id}`);
		await expect(lanes.nth(1)).toHaveAttribute("data-testid", `board-lane-milestone-${ga.id}`);
		await expect(lanes.nth(2)).toHaveAttribute("data-testid", `board-lane-milestone-${later.id}`);
		await expect(lanes.nth(3)).toHaveAttribute("data-testid", "board-lane-unassigned");

		const betaLane = page.getByTestId(`board-lane-milestone-${beta.id}`);
		const gaLane = page.getByTestId(`board-lane-milestone-${ga.id}`);
		const laterLane = page.getByTestId(`board-lane-milestone-${later.id}`);
		const unassignedLane = page.getByTestId("board-lane-unassigned");

		await expect(betaLane).toContainText(`${suffix} multi`);
		await expect(gaLane).toContainText(`${suffix} alpha`);
		await expect(unassignedLane).toContainText(`${suffix} unassigned`);

		// Assign a previously unassigned card by dragging it into the GA lane.
		const gaColumn = gaLane.locator('[data-testid="board-column"]').first();
		await dragCardTo(page, boardCard(page, unassigned.id), gaColumn);
		await expect(gaLane).toContainText(`${suffix} unassigned`);
		await expect
			.poll(() => itemMilestoneIds(request, unassigned.id))
			.toEqual([ga.id]);

		// Drag it back out to Unassigned.
		const unassignedColumn = unassignedLane.locator('[data-testid="board-column"]').first();
		await dragCardTo(page, boardCard(page, unassigned.id), unassignedColumn);
		await expect(unassignedLane).toContainText(`${suffix} unassigned`);
		await expect.poll(() => itemMilestoneIds(request, unassigned.id)).toEqual([]);

		// A multi-milestone card asks before losing attachments.
		const laterColumn = laterLane.locator('[data-testid="board-column"]').first();
		await dragCardTo(page, boardCard(page, multi.id), laterColumn);
		await expect(page.getByTestId("dialog-confirm")).toBeVisible();
		await expect(page.locator('#dialog-description')).toContainText(`${suffix} Beta`);
		await expect(page.locator('#dialog-description')).toContainText(`${suffix} GA`);
		await page.getByTestId("dialog-cancel").click();
		await expect(betaLane).toContainText(`${suffix} multi`);
		await expect.poll(() => itemMilestoneIds(request, multi.id)).toEqual([beta.id, ga.id]);

		await dragCardTo(page, boardCard(page, multi.id), laterColumn);
		await expect(page.getByTestId("dialog-confirm")).toBeVisible();
		await page.getByTestId("dialog-confirm").click();
		await expect(laterLane).toContainText(`${suffix} multi`);
		await expect.poll(() => itemMilestoneIds(request, multi.id)).toEqual([later.id]);

		// Status and milestone can move together inside a milestone lane.
		const gaSecondColumn = gaLane.locator('[data-testid="board-column"]').nth(1);
		await dragCardTo(page, boardCard(page, alpha.id), gaSecondColumn);
		await expect
			.poll(async () => {
				const response = await request.get(`/api/v2/items/${alpha.id}`, { headers: SEC_FETCH });
				const body = await response.json();
				return body.data?.status_id;
			})
			.toBe(statuses[1].id);
		await expect.poll(() => itemMilestoneIds(request, alpha.id)).toEqual([ga.id]);
	});

	test("configures global and completed milestone lanes and persists them", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		await page.setViewportSize({ width: 1600, height: 2600 });
		await authenticateAdminRequest(request);

		const suffix = `bmsc${Date.now()}`;
		const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const statuses = await listWorkspaceStatuses(request, workspace.id);
		const local = await createMilestone(request, workspace.id, `${suffix} Local`, "2026-12-01");
		const global = await createGlobalMilestone(request, `${suffix} Global`, "2026-11-01");
		const shipped = await createMilestone(
			request,
			workspace.id,
			`${suffix} Shipped`,
			"2026-01-01",
			"completed",
		);
		const localCard = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} local card`,
			status_id: statuses[0].id,
			milestone_ids: [local.id],
		});
		const done = statuses.find((status) => status.name === "Done") ?? statuses[statuses.length - 1];
		// A closed card stays in the hidden-completed aggregate; an open card on
		// the same finished milestone belongs in the rescue lane.
		const shippedCard = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} shipped card`,
			status_id: done.id,
			milestone_ids: [shipped.id],
		});
		const rescueCard = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} rescue card`,
			status_id: statuses[0].id,
			milestone_ids: [shipped.id],
		});

		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId("board-view")).toBeVisible({ timeout: 15_000 });
		await selectMilestoneGrouping(page, local.id);

		// The empty global milestone is not shown until it is pinned, while the
		// completed lane keeps its card by default.
		await expect(page.getByTestId(`board-lane-milestone-${global.id}`)).toHaveCount(0);
		await expect(page.getByTestId(`board-lane-milestone-${shipped.id}`)).toContainText(
			`${suffix} shipped card`,
		);

		await page.getByTestId("board-group-by-menu").click();
		await page.getByTestId("board-group-by-configure").click();
		const configDialog = page.getByTestId("milestone-swimlane-config-dialog");
		await expect(configDialog).toBeVisible();
		await configDialog.getByTestId("item-picker-trigger").click();
		const milestoneSearch = page.getByTestId("milestone-swimlanes-search");
		// Only global milestones are offered: the local milestone never appears.
		await milestoneSearch.fill(`${suffix} Local`);
		await expect(page.getByTestId(`milestone-swimlanes-global-${local.id}`)).toHaveCount(0);
		await milestoneSearch.fill(`${suffix} Global`);
		await page.getByTestId(`milestone-swimlanes-global-${global.id}`).click();
		// Multi-select keeps the dropdown open; close it before touching the footer.
		await configDialog.getByTestId("item-picker-trigger").click();
		await page.getByTestId("milestone-swimlanes-show-completed").click();
		await page.getByTestId("milestone-swimlanes-show-open-from-completed").click();
		await page.getByTestId("milestone-swimlanes-save").click();
		await expect(page.getByTestId("milestone-swimlane-config-dialog")).toHaveCount(0);

		// The pinned global lane appears, the hidden completed milestone keeps its
		// closed card in the aggregate lane, and its open card surfaces in the
		// dedicated rescue lane.
		await expect(page.getByTestId(`board-lane-milestone-${global.id}`)).toBeVisible();
		await expect(page.getByTestId(`board-lane-milestone-${shipped.id}`)).toHaveCount(0);
		const hiddenLane = page.getByTestId("board-lane-milestone-completed-hidden");
		await expect(hiddenLane).toContainText(`${suffix} shipped card`);
		await expect(hiddenLane).not.toContainText(`${suffix} rescue card`);
		const rescueLane = page.getByTestId("board-lane-milestone-open-from-completed");
		await expect(rescueLane).toContainText(`${suffix} rescue card`);
		await expect(page.getByTestId(`board-lane-milestone-${local.id}`)).toContainText(
			`${suffix} local card`,
		);

		// The preference survives a reload and reopens in the dialog.
		const configResponse = await request.get(
			`/api/v2/workspaces/${workspace.id}/board-configuration`,
			{ headers: SEC_FETCH },
		);
		const configBody = await configResponse.json();
		expect(
			configBody.data?.view_settings?.milestone_swimlanes?.global_milestone_ids,
		).toContain(global.id);
		await page.reload();
		await expect(page.getByTestId("board-view")).toBeVisible({ timeout: 15_000 });
		await expect(page.getByTestId(`board-lane-milestone-${global.id}`)).toBeVisible();
		await expect(page.getByTestId(`board-lane-milestone-${shipped.id}`)).toHaveCount(0);
		await expect(page.getByTestId("board-lane-milestone-open-from-completed")).toContainText(
			`${suffix} rescue card`,
		);

		await page.getByTestId("board-group-by-menu").click();
		await page.getByTestId("board-group-by-configure").click();
		const reopenedDialog = page.getByTestId("milestone-swimlane-config-dialog");
		await expect(reopenedDialog.getByTestId("item-picker-trigger")).toContainText(
			`${suffix} Global`,
		);
		await expect(page.getByTestId("milestone-swimlanes-show-completed-input")).not.toBeChecked();
		await expect(
			page.getByTestId("milestone-swimlanes-show-open-from-completed-input"),
		).toBeChecked();
		await page.getByTestId("milestone-swimlanes-cancel").click();

		expect(localCard.id).toBeGreaterThan(0);
		expect(shippedCard.id).toBeGreaterThan(0);
		expect(rescueCard.id).toBeGreaterThan(0);
	});

	test("renders owner-configured milestone lanes on the read-only public board", async ({
		page,
		request,
	}) => {
		await authenticateAdminRequest(request);

		const suffix = `pms${Date.now()}`;
		const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const statuses = await listWorkspaceStatuses(request, workspace.id);
		const beta = await createMilestone(request, workspace.id, `${suffix} Beta`, "2026-11-01");
		const ga = await createMilestone(request, workspace.id, `${suffix} GA`, "2026-12-01");
		const assigned = await createItemViaAPI(request, workspace.id, {
			title: `${suffix} assigned`,
			status_id: statuses[0].id,
			milestone_ids: [beta.id],
		});
		await createItemViaAPI(request, workspace.id, {
			title: `${suffix} unassigned`,
			status_id: statuses[0].id,
		});

		const slug = `public-milestones-${Date.now()}`;
		const collectionResponse = await request.post("/api/v2/collections", {
			headers: SEC_FETCH,
			data: {
				name: `${suffix} public`,
				ql_query: `workspace_id = ${workspace.id}`,
				workspace_id: workspace.id,
				is_public: true,
				public_slug: slug,
			},
		});
		expect(
			collectionResponse.status(),
			`create collection: ${await collectionResponse.text()}`,
		).toBe(201);
		const collectionId = (await collectionResponse.json()).data.id;
		const enableResponse = await request.patch(`/api/v2/collections/${collectionId}/sharing`, {
			headers: { ...SEC_FETCH, "Content-Type": "application/merge-patch+json" },
			data: {
				is_public: true,
				public_slug: slug,
				public_milestone_swimlanes: true,
			},
		});
		expect(enableResponse.ok(), `enable swimlanes: ${await enableResponse.text()}`).toBeTruthy();

		await page.goto(`/board/${slug}`);
		await expect(page.getByTestId("public-board-page")).toHaveAttribute("data-ready", "true", {
			timeout: 15_000,
		});
		await expect(page.getByTestId("public-board-swimlanes")).toBeVisible();
		await expect(page.getByTestId(`public-board-lane-milestone-${beta.id}`)).toContainText(
			`${suffix} assigned`,
		);
		await expect(page.getByTestId(`public-board-lane-${ga.id}`)).toHaveCount(0);
		const unassignedLane = page.getByTestId("public-board-lane-none");
		await expect(unassignedLane).toContainText(`${suffix} unassigned`);
		// Read-only: no grouping control and no draggable board cards.
		await expect(page.getByTestId("board-group-by-menu")).toHaveCount(0);
		await expect(page.getByTestId("public-board-card").first()).toBeVisible();

		const disableResponse = await request.patch(`/api/v2/collections/${collectionId}`, {
			headers: { ...SEC_FETCH, "Content-Type": "application/merge-patch+json" },
			data: { public_milestone_swimlanes: false },
		});
		expect(disableResponse.ok()).toBeTruthy();

		await page.reload();
		await expect(page.getByTestId("public-board-page")).toHaveAttribute("data-ready", "true", {
			timeout: 15_000,
		});
		await expect(page.getByTestId("public-board-swimlanes")).toHaveCount(0);
		await expect(page.getByTestId("public-board-column").first()).toBeVisible();
		// The assigned card still exists and is shown by its status column.
		const publicCardTexts = await page.getByTestId("public-board-card").allTextContents();
		expect(publicCardTexts.some((text) => text.includes(`${suffix} assigned`))).toBe(true);
		expect(assigned.id).toBeGreaterThan(0);
	});
});
