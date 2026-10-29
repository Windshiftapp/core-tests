import { randomUUID } from "node:crypto";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
	listItemTypesViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { installCapabilityPlugin, deleteCapabilityPlugins } from "../fixtures/plugin-zip";
import { generateWorkspace } from "../fixtures/test-data";
import { preferenceSave } from "../helpers/preferences";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

type ItemType = { id: number; hierarchy_level: number };

async function createTypedItem(
	request: Parameters<typeof createItemViaAPI>[0],
	workspaceId: number,
	itemTypeId: number | undefined,
	title: string,
	extra: Record<string, unknown> = {},
) {
	const item = await createItemViaAPI(request, workspaceId, {
		title,
		item_type_id: itemTypeId,
		...extra,
	});
	return item as { id: number };
}

async function selectSwimlaneDimension(page: import("@playwright/test").Page, value: string) {
	await page.getByTestId("map-swimlanes-button").click();
	await expect(page.getByTestId("map-swimlanes-panel")).toBeVisible();
	await page.locator("#map-swimlane-dimension").click();
	const saved = preferenceSave(page);
	await page.locator(`#map-swimlane-dimension-option-${value}`).click();
	await saved;
}

test("map swimlanes stay hidden without the plugin and group by iteration and milestone once installed", async ({
	page,
	request,
}) => {
	const suffix = `map-lanes-${randomUUID().slice(0, 8)}`;
	const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
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

	const parent = await createTypedItem(
		request,
		workspace.id,
		parentType.id,
		`${suffix} parent`,
	);
	const childType = itemTypes.find(
		(candidate) => candidate.hierarchy_level === parentType.hierarchy_level + 1,
	);
	const childIteration = await createTypedItem(
		request,
		workspace.id,
		childType?.id,
		`${suffix} iterated child`,
		{ parent_id: parent.id },
	);
	const childPlain = await createTypedItem(
		request,
		workspace.id,
		childType?.id,
		`${suffix} plain child`,
		{ parent_id: parent.id },
	);

	// Local iteration via the workspace-scoped planning endpoint
	const iterationResponse = await request.post(
		`/api/v2/workspaces/${workspace.id}/iterations`,
		{
			headers: SEC_FETCH,
			data: {
				name: `${suffix} sprint`,
				start_date: "2026-09-01",
				end_date: "2026-09-14",
			},
		},
	);
	expect(
		iterationResponse.ok(),
		`create iteration: ${iterationResponse.status()} ${await iterationResponse.text()}`,
	).toBeTruthy();
	const iteration = (await iterationResponse.json()).data as { id: number };

	const patchIteration = await request.patch(`/api/v2/items/${childIteration.id}`, {
		headers: { ...SEC_FETCH, "Content-Type": "application/merge-patch+json" },
		data: { iteration_id: iteration.id },
	});
	expect(
		patchIteration.ok(),
		`assign iteration: ${patchIteration.status()} ${await patchIteration.text()}`,
	).toBeTruthy();

	// Global milestone from the unscoped planning endpoint, attached to its own child
	const milestoneResponse = await request.post("/api/v2/milestones", {
		headers: SEC_FETCH,
		data: { name: `${suffix} milestone`, target_date: "2026-12-01" },
	});
	expect(
		milestoneResponse.ok(),
		`create milestone: ${milestoneResponse.status()} ${await milestoneResponse.text()}`,
	).toBeTruthy();
	const milestone = (await milestoneResponse.json()).data as { id: number };

	const childMilestone = await createTypedItem(
		request,
		workspace.id,
		childType?.id,
		`${suffix} milestone child`,
		{ parent_id: parent.id, milestone_ids: [milestone.id] },
	);

	// Uploaded plugins persist across runs on the server's plugin directory;
	// clear leftovers so the denial phase really starts without the capability.
	await deleteCapabilityPlugins(request, "e2e-map-swimlanes-");

	// ── Phase 1: capability denied — no swimlanes control at all ──
	await page.goto(`/workspaces/${workspace.id}/map`);
	await expect(page.getByTestId("map-view")).toBeVisible();
	await expect(page.getByTestId(`map-backbone-item-${parent.id}`)).toBeVisible();
	await expect(page.getByTestId("map-swimlanes-button")).toHaveCount(0);

	// ── Phase 2: install the capability plugin ──
	await installCapabilityPlugin(request, `e2e-map-swimlanes-${randomUUID().slice(0, 8)}`, [
		"map.swimlanes",
	]);
	await page.reload();
	await expect(page.getByTestId("map-swimlanes-button")).toBeVisible();

	// Iteration lanes: assigned child in its lane, plain child in none lane
	await selectSwimlaneDimension(page, "iteration");
	await expect(page.getByTestId(`map-lane-header-iteration-${iteration.id}`)).toContainText(
		`${suffix} sprint`,
	);
	await expect(
		page
			.getByTestId(`map-lane-cell-${parent.id}-iteration-${iteration.id}`)
			.getByTestId(`draggable-item-${childIteration.id}`),
	).toBeVisible();
	await expect(
		page
			.getByTestId(`map-lane-cell-${parent.id}-none`)
			.getByTestId(`draggable-item-${childPlain.id}`),
	).toBeVisible();

	// Dimension choice survives a reload
	await page.reload();
	await expect(page.getByTestId(`map-lane-header-iteration-${iteration.id}`)).toBeVisible();

	// Milestone lanes fold global milestones in
	await selectSwimlaneDimension(page, "milestone");
	await expect(page.getByTestId(`map-lane-header-milestone-${milestone.id}`)).toContainText(
		`${suffix} milestone`,
	);
	await expect(
		page
			.getByTestId(`map-lane-cell-${parent.id}-milestone-${milestone.id}`)
			.getByTestId(`draggable-item-${childMilestone.id}`),
	).toBeVisible();
	await expect(page.getByTestId("map-lane-header-none")).toBeVisible();

	// Collapsing a lane hides its cells until expanded again
	await selectSwimlaneDimension(page, "iteration");
	await page.getByTestId(`map-lane-header-iteration-${iteration.id}`).click();
	await expect(
		page.getByTestId(`map-lane-cell-${parent.id}-iteration-${iteration.id}`),
	).toHaveCount(0);
	await page.getByTestId(`map-lane-header-iteration-${iteration.id}`).click();
	await expect(
		page.getByTestId(`map-lane-cell-${parent.id}-iteration-${iteration.id}`),
	).toBeVisible();

	// Dragging a card into a lane applies the attribute. Both endpoints must be
	// on screen: the map is taller than the viewport and mouse coordinates are
	// viewport-relative, so a card below the fold would hit the footer instead.
	const source = page.getByTestId(`draggable-item-${childPlain.id}`);
	const target = page.getByTestId(`map-lane-cell-${parent.id}-iteration-${iteration.id}`);
	await source.scrollIntoViewIfNeeded();
	await target.scrollIntoViewIfNeeded();
	const sourceBox = await source.boundingBox();
	const targetBox = await target.boundingBox();
	const viewHeight = page.viewportSize()?.height ?? 720;
	if (
		!sourceBox ||
		!targetBox ||
		sourceBox.y < 0 ||
		sourceBox.y + sourceBox.height > viewHeight ||
		targetBox.y < 0 ||
		targetBox.y + targetBox.height > viewHeight
	) {
		throw new Error("swimlane drag source/target not fully in viewport after scrolling");
	}
	const lanePatchPromise = page.waitForResponse(
		(response) =>
			response.url().endsWith(`/api/v2/items/${childPlain.id}`) &&
			response.request().method() === "PATCH",
	);
	await page.mouse.move(sourceBox.x + sourceBox.width / 2, sourceBox.y + sourceBox.height / 2);
	await page.mouse.down();
	await page.mouse.move(sourceBox.x + 12, sourceBox.y + 12);
	await page.mouse.move(targetBox.x + targetBox.width / 2, targetBox.y + 12, { steps: 12 });
	await page.mouse.up();
	const lanePatch = await lanePatchPromise;
	expect(
		lanePatch.ok(),
		`swimlane lane move: ${lanePatch.status()} ${await lanePatch.text()}`,
	).toBeTruthy();

	await expect(
		page
			.getByTestId(`map-lane-cell-${parent.id}-iteration-${iteration.id}`)
			.getByTestId(`draggable-item-${childPlain.id}`),
	).toBeVisible({ timeout: 10_000 });

	// The lane move persisted
	await page.reload();
	await expect(page.getByTestId(`map-lane-header-iteration-${iteration.id}`)).toBeVisible();
	await expect(
		page
			.getByTestId(`map-lane-cell-${parent.id}-iteration-${iteration.id}`)
			.getByTestId(`draggable-item-${childPlain.id}`),
	).toBeVisible();

	const laneCount = page.getByTestId(`map-lane-count-iteration-${iteration.id}`);
	await expect(laneCount).toBeVisible();

	// Cleanup: uninstall the capability plugin
	await deleteCapabilityPlugins(request, "e2e-map-swimlanes-");
});
