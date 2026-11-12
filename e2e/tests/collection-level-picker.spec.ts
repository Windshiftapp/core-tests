import { randomUUID } from "node:crypto";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
	listItemTypesViaAPI,
	listWorkspaceItemTypesViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

type ItemType = { id: number; name: string; hierarchy_level: number };

/**
 * The level picker is the control that scopes tree/map/roadmap to one item-type
 * level. It had no browser coverage, which is how the map's add card could
 * break at level 0 without a test noticing.
 */
test("level picker scopes the map, shows counts, and persists the choice", async ({
	page,
	request,
}) => {
	const suffix = randomUUID().slice(0, 8);
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(`level-picker-${suffix}`),
	);
	const globalTypes = (await listItemTypesViaAPI(request)) as ItemType[];
	const workspaceTypes = (await listWorkspaceItemTypesViaAPI(
		request,
		workspace.id,
	)) as ItemType[];
	const byLevel = (level: number) =>
		workspaceTypes.find((type) => type.hierarchy_level === level);
	const initiativeType = globalTypes.find(
		(type) => type.hierarchy_level === 0,
	);
	if (!initiativeType) throw new Error("No level-0 item type in the catalog");

	// The default workspace configuration set excludes Initiative, so this
	// level-0 type is deliberately not workspace-bound.
	expect(workspaceTypes.some((type) => type.id === initiativeType.id)).toBe(
		false,
	);

	const initiative = await createItemViaAPI(request, workspace.id, {
		title: `Level initiative ${suffix}`,
		item_type_id: initiativeType.id,
	});
	const epic = await createItemViaAPI(request, workspace.id, {
		title: `Level epic ${suffix}`,
		item_type_id: byLevel(1)?.id,
	});
	await createItemViaAPI(request, workspace.id, {
		title: `Level story ${suffix}`,
		item_type_id: byLevel(2)?.id,
		parent_id: epic.id,
	});

	await page.goto(`/workspaces/${workspace.id}/map`);
	await expect(page.getByTestId("map-view")).toBeVisible();

	// Level 0 is the shallowest level with matches, so it is the default view.
	const trigger = page.getByTestId("hierarchy-level-trigger");
	await expect(trigger).toContainText(initiativeType.name);
	await expect(
		page.getByTestId(`map-backbone-item-${initiative.id}`),
	).toBeVisible();
	await expect(page.getByTestId(`map-backbone-item-${epic.id}`)).toHaveCount(
		0,
	);

	// Every level is offered with its filtered count.
	await trigger.click();
	await expect(page.getByTestId("hierarchy-level-option-0")).toContainText(
		"1 item",
	);
	await expect(page.getByTestId("hierarchy-level-option-1")).toContainText(
		"1 item",
	);

	// Switching levels re-roots the backbone.
	const levelSaved = page.waitForResponse((response) => {
		if (
			!response.url().includes("/api/v2/users/me/preferences") ||
			response.request().method() !== "PATCH"
		) {
			return false;
		}
		return (response.request().postData() ?? "").includes("hierarchy_level");
	});
	await page.getByTestId("hierarchy-level-option-1").click();
	await expect(
		page.getByTestId(`map-backbone-item-${epic.id}`),
	).toBeVisible();
	await expect(
		page.getByTestId(`map-backbone-item-${initiative.id}`),
	).toHaveCount(0);

	// The choice is persisted per user, so a reload keeps it. Wait for the
	// debounced preferences PATCH before reloading.
	await levelSaved;
	await page.reload();
	await expect(page.getByTestId("map-view")).toBeVisible();
	await expect(page.getByTestId("hierarchy-level-trigger")).toContainText(
		"Epic",
	);
	await expect(
		page.getByTestId(`map-backbone-item-${epic.id}`),
	).toBeVisible();
});
