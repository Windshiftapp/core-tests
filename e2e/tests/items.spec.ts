import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateItem, generateWorkspace } from "../fixtures/test-data";
import { ItemPage } from "../pages/item.page";

/**
 * Work Item Management Tests
 * Tests item CRUD operations and hierarchy using authenticated context
 */

test.describe("Item Management", () => {
	let itemPage: ItemPage;
	let workspaceId: string;

	test.beforeEach(async ({ page, request }) => {
		itemPage = new ItemPage(page);
		const workspace = await createWorkspaceViaAPI(request, generateWorkspace());
		workspaceId = String(workspace.id);
	});

	test.describe("Create Item", () => {
		test("should create basic item", async () => {
			const item = generateItem(0, "basic");

			await itemPage.createItem(workspaceId, {
				title: item.title,
				description: item.description,
			});

			// Verify item was created
			await itemPage.verifyItemExists(item.title);
		});

		test("should create item with minimal data", async () => {
			const item = generateItem(0, "minimal");

			await itemPage.createItem(workspaceId, {
				title: item.title,
			});

			// Verify item was created
			await itemPage.verifyItemExists(item.title);
		});

		test("should require item title", async ({ page }) => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.clickCreate();

			// Submit button should be disabled when title is empty
			const submitBtn = page.locator(itemPage.saveButton);
			await expect(submitBtn).toBeDisabled();

			// Modal should still be visible
			const modal = page.locator(itemPage.itemModal);
			await expect(modal).toBeVisible();
		});

		test("should display created item in backlog", async () => {
			const item = generateItem(0, "display");

			await itemPage.createItem(workspaceId, {
				title: item.title,
				description: item.description,
			});

			// Navigate to backlog
			await itemPage.gotoWorkspaceBacklog(workspaceId);

			// Find item
			const itemElement = await itemPage.findItemByTitle(item.title);
			await expect(itemElement).toBeVisible();
		});
	});

	test.describe("View Item", () => {
		let testItem: ReturnType<typeof generateItem>;

		test.beforeEach(async () => {
			testItem = generateItem(0, "view");
			await itemPage.createItem(workspaceId, {
				title: testItem.title,
				description: testItem.description,
			});
		});

		test("should view item details", async () => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.clickItem(testItem.title);

			// Item detail opens as a modal overlay on the backlog.
			await expect(itemPage.page.getByTestId("item-title-edit")).toHaveText(
				testItem.title,
			);
		});

		test("should display item fields", async () => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.clickItem(testItem.title);

			await expect(
				itemPage.page.getByTestId("item-detail-ready"),
			).toBeVisible();
		});
	});

	test.describe("Edit Item", () => {
		let testItem: ReturnType<typeof generateItem>;

		test.beforeEach(async () => {
			testItem = generateItem(0, "edit");
			await itemPage.createItem(workspaceId, {
				title: testItem.title,
				description: testItem.description,
			});
		});

		test("should update item title", async () => {
			const newTitle = `${testItem.title} - Updated`;
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(testItem.title);
			await itemPage.editTitleInline(newTitle);
			await itemPage.closeItemDetailModal();

			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.verifyItemExists(newTitle);
		});

		// Persistence helper: list workspace items through the canonical API and
		// return the one whose title matches after a picker update.
		async function fetchItemByTitle(title: string) {
			const resp = await itemPage.page.request.get(
				`/api/v2/items?workspace_id=${workspaceId}`,
			);
			expect(resp.ok(), `list items failed: ${resp.status()}`).toBeTruthy();
			const body = await resp.json();
			const items = (body.data ?? body.items ?? body) as Array<{
				id: number;
				title: string;
				status_name?: string;
				status?: { name?: string };
				priority_name?: string;
				priority?: { name?: string };
			}>;
			const match = items.find((i) => i.title === title);
			if (!match) throw new Error(`item "${title}" missing from items list`);
			return match;
		}

		test("should update item status", async () => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(testItem.title);
			await itemPage.changeStatus("In Progress");
			await itemPage.closeItemDetailModal();

			// Poll the API for the persisted status: the picker updates optimistically,
			// so the PATCH may still be in flight when we first read back. expect.poll
			// re-fetches until the server reflects the change (or times out).
			await expect
				.poll(
					async () => {
						const persisted = await fetchItemByTitle(testItem.title);
						return persisted.status_name ?? persisted.status?.name;
					},
					{ timeout: 5000 },
				)
				.toBe("In Progress");
		});

		test("should update item priority", async () => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(testItem.title);
			await itemPage.changePriority("High");
			await itemPage.closeItemDetailModal();

			// Poll for the persisted priority — same optimistic-update race as status.
			await expect
				.poll(
					async () => {
						const persisted = await fetchItemByTitle(testItem.title);
						return persisted.priority_name ?? persisted.priority?.name;
					},
					{ timeout: 5000 },
				)
				.toBe("High");
		});
	});

	test.describe("Delete Item", () => {
		let testItem: ReturnType<typeof generateItem>;

		test.beforeEach(async () => {
			testItem = generateItem(0, "delete");
			await itemPage.createItem(workspaceId, {
				title: testItem.title,
				description: testItem.description,
			});
		});

		test("should delete item", async () => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(testItem.title);
			await itemPage.deleteItemViaModal();
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.verifyItemDoesNotExist(testItem.title);
		});

		test("should show delete confirmation dialog", async () => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(testItem.title);
			const dialog = itemPage.page.locator('[role="dialog"]');
			await dialog
				.locator('[data-testid="item-detail-actions"] button')
				.first()
				.click();
			await itemPage.page
				.locator('button[role="menuitem"]')
				.filter({ hasText: /delete/i })
				.click();
			await expect(
				itemPage.page.locator('[data-testid="delete-item-dialog"]'),
			).toBeVisible({ timeout: 5000 });
		});
	});

	test.describe("Item Hierarchy", () => {
		let parentItem: ReturnType<typeof generateItem>;

		test.beforeEach(async () => {
			parentItem = generateItem(0, "parent");
			await itemPage.createItem(workspaceId, {
				title: parentItem.title,
				description: "Parent item for hierarchy testing",
			});
		});

		test("preserves parent context while the create modal module loads", async ({
			page,
		}) => {
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(parentItem.title);
			await page.clock.install();
			let releaseModule!: () => void;
			let moduleRequested!: () => void;
			const release = new Promise<void>((resolve) => {
				releaseModule = resolve;
			});
			const requested = new Promise<void>((resolve) => {
				moduleRequested = resolve;
			});
			await page.route(/\/CreateModal-[^/]+\.js$/, async (route) => {
				moduleRequested();
				await release;
				await route.continue();
			});
			try {
				await page.getByTestId("item-create-child").click();
				await requested;
				// Exercise the old 150 ms parent event while its recipient is still unloaded.
				await page.clock.runFor(500);
				releaseModule();
				await expect(page.getByTestId("create-modal-heading")).toHaveText(
					"New Child Item",
				);
				await page.locator("#work-item-title").fill("Delayed child");
				await page.locator("#create-modal-submit").click();
				await expect(page.getByTestId("create-modal")).toHaveCount(0);
				await expect(page.getByTestId("item-detail-ready")).toContainText(
					parentItem.title,
				);
				await expect(page.getByTestId("item-detail-ready")).toContainText(
					"Delayed child",
				);
			} finally {
				releaseModule();
				await page.unroute(/\/CreateModal-[^/]+\.js$/);
			}
		});

		test("should create child item", async () => {
			const childTitle = `Child of ${parentItem.title}`;
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(parentItem.title);
			await itemPage.createChildItemViaModal(childTitle);
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.verifyItemExists(childTitle);
		});

		test("should display child items in parent detail", async () => {
			const childTitle = `Child of ${parentItem.title}`;
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(parentItem.title);
			await itemPage.createChildItemViaModal(childTitle);

			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(parentItem.title);
			const childCard = itemPage.page
				.locator("[data-child-item-card]")
				.filter({ hasText: childTitle });
			await expect(childCard).toBeVisible({ timeout: 10000 });
		});

		test("should stay on parent and show new child in list after creating", async () => {
			const childTitle = `Child of ${parentItem.title}`;
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(parentItem.title);
			const parentDialog = itemPage.page.locator('[role="dialog"]').first();
			await expect(parentDialog.getByTestId("item-title-edit")).toContainText(
				parentItem.title,
			);

			await itemPage.createChildItemViaModal(childTitle);

			// Parent detail modal must still be open and showing the parent (no navigation away)
			await expect(itemPage.page.getByText("Work Item Details")).toBeVisible({
				timeout: 5000,
			});
			await expect(parentDialog.getByTestId("item-title-edit")).toContainText(
				parentItem.title,
			);

			// Child appears in the parent's children list without re-navigation
			const childCard = parentDialog
				.locator("[data-child-item-card]")
				.filter({ hasText: childTitle });
			await expect(childCard).toBeVisible({ timeout: 10000 });
		});

		test("should delete parent and cascade to children", async () => {
			const childTitle = `Child of ${parentItem.title}`;
			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(parentItem.title);
			await itemPage.createChildItemViaModal(childTitle);

			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.openItemDetailModal(parentItem.title);
			await itemPage.page.getByTestId("item-detail-actions-menu").click();
			await itemPage.page.getByTestId("item-delete-open").click();
			const deleteDialog = itemPage.page.getByTestId("delete-item-dialog");
			await deleteDialog.waitFor({ state: "visible", timeout: 5000 });
			await deleteDialog
				.getByTestId("delete-item-confirmation")
				.fill(parentItem.title);
			const confirmDelete = deleteDialog.getByTestId("delete-item-confirm");
			await expect(confirmDelete).toBeEnabled();
			await confirmDelete.click();
			await deleteDialog.waitFor({ state: "detached", timeout: 10000 });

			await itemPage.gotoWorkspaceBacklog(workspaceId);
			await itemPage.verifyItemDoesNotExist(parentItem.title);
			await itemPage.verifyItemDoesNotExist(childTitle);
		});
	});

	test.describe("Item List", () => {
		test("should display multiple items", async () => {
			const item1 = generateItem(0, "1");
			const item2 = generateItem(0, "2");
			const item3 = generateItem(0, "3");

			await itemPage.createItem(workspaceId, { title: item1.title });
			await itemPage.createItem(workspaceId, { title: item2.title });
			await itemPage.createItem(workspaceId, { title: item3.title });

			await itemPage.gotoWorkspaceBacklog(workspaceId);

			await itemPage.verifyItemExists(item1.title);
			await itemPage.verifyItemExists(item2.title);
			await itemPage.verifyItemExists(item3.title);

			// The create submits re-fetch the backlog; wait (bounded) for the
			// rows to render.
			const countStart = Date.now();
			await expect
				.poll(async () => itemPage.getItemCount(), { timeout: 15000 })
				.toBeGreaterThanOrEqual(3);
		});

		// Status filtering lives on the Collection list view (WorkItemFilterPanel),
		// not on the workspace backlog. The previous version of this test waited
		// for `select[name="status"]` on the backlog and silently passed when the
		// selector wasn't found, so it never actually exercised filtering.
		// Re-introducing it belongs on the collection-list route under a dedicated
		// workspace + items setup, not here.
	});
});
