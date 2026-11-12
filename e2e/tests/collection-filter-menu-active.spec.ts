import { expect, test } from "../fixtures/context-path";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * The unified Filter menu must surface the applied filter, let the user clear
 * it, and clear it when the saved filter it came from is deleted. Before the
 * fix the menu listed saved filters only: the applied state was invisible and
 * deleting the saved filter left a filter that could not be unselected.
 */
test("filter menu shows the applied filter and clears it when its saved filter is deleted", async ({
	page,
	request,
}) => {
	const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(`filter-active-${stamp}`),
	);

	const needle = await createItemViaAPI(request, workspace.id, {
		title: `Needle ${stamp}`,
	});
	const other = await createItemViaAPI(request, workspace.id, {
		title: `Other ${stamp}`,
	});
	const needleKey = `${workspace.key}-${needle.workspace_item_number}`;

	await page.goto(`/workspaces/${workspace.id}/list`);
	await expect(page.getByTestId("list-view")).toBeVisible();
	const needleRow = page.getByTestId(`workspace-item-row-${needle.id}`);
	const otherRow = page.getByTestId(`workspace-item-row-${other.id}`);
	await expect(needleRow).toBeVisible();
	await expect(otherRow).toBeVisible();

	// Build and apply a Key filter through the builder.
	await page.getByTestId("dynamic-filter-toggle").click();
	await page.getByTestId("filter-menu-add").click();
	await page.getByTestId("field-selector-trigger").click();
	await page.getByTestId("field-option-key").click();
	await page.getByTestId("collection-dynamic-filter-0-value").click();
	await page
		.getByTestId("collection-dynamic-filter-0-value-input")
		.fill(needleKey);
	await page.getByTestId("collection-dynamic-filter-0-apply-value").click();

	await expect(needleRow).toBeVisible();
	await expect(otherRow).toHaveCount(0);

	// The applied filter is visible in the menu and can be cleared.
	await page.getByTestId("dynamic-filter-toggle").click();
	await expect(page.getByTestId("filter-menu-active")).toBeVisible();
	await expect(page.getByTestId("filter-menu-active-row-0")).toContainText(
		needleKey,
	);

	// Save it as a personal filter, which keeps it applied.
	await page.getByTestId("filter-menu-save").click();
	await page.getByTestId("filter-builder-name").fill(`Saved ${stamp}`);
	await page.getByTestId("filter-builder-save").click();

	await page.getByTestId("dynamic-filter-toggle").click();
	const savedRow = page.locator('[data-testid^="filter-menu-personal-apply-"]');
	await expect(savedRow).toBeVisible();
	await expect(
		page.locator('[data-testid^="filter-menu-personal-applied-"]'),
	).toBeVisible();

	// Deleting the applied saved filter clears the live filter too.
	await page
		.locator('[data-testid^="filter-menu-personal-delete-"]')
		.first()
		.click();
	await expect(page.getByTestId("filter-menu-active")).toHaveCount(0);
	await expect(needleRow).toBeVisible();
	await expect(otherRow).toBeVisible();
});
