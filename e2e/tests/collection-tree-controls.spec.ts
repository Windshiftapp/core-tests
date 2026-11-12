import { randomUUID } from "node:crypto";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
	listWorkspaceItemTypesViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

type ItemType = { id: number; hierarchy_level: number };

/**
 * The tree's Expand All / Show Tests controls had no stable selectors and no
 * browser coverage, so a regression there was invisible.
 */
test("tree controls expand the whole tree and persist the test-case toggle", async ({
	page,
	request,
}) => {
	const suffix = randomUUID().slice(0, 8);
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(`tree-controls-${suffix}`),
	);
	const workspaceTypes = (await listWorkspaceItemTypesViaAPI(
		request,
		workspace.id,
	)) as ItemType[];
	const byLevel = (level: number) =>
		workspaceTypes.find((type) => type.hierarchy_level === level);

	const epic = await createItemViaAPI(request, workspace.id, {
		title: `Tree epic ${suffix}`,
		item_type_id: byLevel(1)?.id,
	});
	const story = await createItemViaAPI(request, workspace.id, {
		title: `Tree story ${suffix}`,
		item_type_id: byLevel(2)?.id,
		parent_id: epic.id,
	});
	const task = await createItemViaAPI(request, workspace.id, {
		title: `Tree task ${suffix}`,
		item_type_id: byLevel(3)?.id,
		parent_id: story.id,
	});

	await page.goto(`/workspaces/${workspace.id}/tree`);
	await expect(page.getByTestId("tree-view")).toBeVisible();
	await expect(page.getByTestId(`tree-row-${epic.id}`)).toBeVisible();
	await expect(page.getByTestId(`tree-row-${story.id}`)).toBeVisible();
	await expect(page.getByTestId(`tree-row-${task.id}`)).toHaveCount(0);

	// Roots auto-expand, so the first click collapses the whole tree.
	const expandToggle = page.getByTestId("tree-expand-toggle");
	await expandToggle.click();
	await expect(page.getByTestId(`tree-row-${story.id}`)).toHaveCount(0);

	// The second click expands every loaded level.
	await expandToggle.click();
	await expect(page.getByTestId(`tree-row-${task.id}`)).toBeVisible();

	// The test-case toggle is persisted per user.
	const showTests = page.getByTestId("tree-show-tests");
	await expect(showTests).toHaveAttribute("aria-pressed", "false");
	const toggleSaved = page.waitForResponse((response) => {
		if (
			!response.url().includes("/api/v2/users/me/preferences") ||
			response.request().method() !== "PATCH"
		) {
			return false;
		}
		return (response.request().postData() ?? "").includes("show_test_cases");
	});
	await showTests.click();
	await expect(showTests).toHaveAttribute("aria-pressed", "true");

	// Wait for the debounced preferences PATCH before reloading.
	await toggleSaved;
	await page.reload();
	await expect(page.getByTestId("tree-view")).toBeVisible();
	await expect(page.getByTestId("tree-show-tests")).toHaveAttribute(
		"aria-pressed",
		"true",
	);
});
