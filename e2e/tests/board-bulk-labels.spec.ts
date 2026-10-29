import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import {
	type APIRequestContext,
	expect,
	test,
} from "../fixtures/context-path";
import { generateItem, generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

async function createLabelViaAPI(
	request: APIRequestContext,
	workspaceId: number,
	name: string,
) {
	const response = await request.post(
		`/api/v2/workspaces/${workspaceId}/labels`,
		{ headers: SEC_FETCH, data: { name } },
	);
	expect(
		response.ok(),
		`create label failed (${response.status()}): ${await response.text()}`,
	).toBeTruthy();
	return (await response.json()).data as { id: number; name: string };
}

async function setItemLabelsViaAPI(
	request: APIRequestContext,
	itemId: number,
	labelIds: number[],
) {
	const response = await request.put(`/api/v2/items/${itemId}/labels`, {
		headers: SEC_FETCH,
		data: { label_ids: labelIds },
	});
	expect(
		response.ok(),
		`set item labels failed (${response.status()}): ${await response.text()}`,
	).toBeTruthy();
}

/**
 * Bulk label editing from the board selection toolbar.
 *
 * The toolbar used to render a dropdown of pre-existing labels and disabled
 * itself when the workspace had none, so there was no way to create a label
 * from the board. The flow now opens a picker modal that can create labels and
 * applies additions/removals to every selected item.
 */

test.describe.configure({ retries: 0 });

test.describe("Board bulk labels", () => {
	test("creates a label from the modal and applies it to the selection", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		await authenticateAdminRequest(request);

		const suffix = `bbl${Date.now()}`;
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const first = await createItemViaAPI(request, ws.id, {
			title: generateItem(ws.id, `${suffix}-a`).title,
		});
		const second = await createItemViaAPI(request, ws.id, {
			title: generateItem(ws.id, `${suffix}-b`).title,
		});
		const labelName = `bulk-created-${suffix}`;

		try {
			await page.goto(`/workspaces/${ws.id}/board`);
			const firstCard = page.getByTestId(`board-item-${first.id}`);
			const secondCard = page.getByTestId(`board-item-${second.id}`);
			await expect(firstCard).toBeVisible({ timeout: 15_000 });
			await expect(secondCard).toBeVisible();

			await page.getByTestId("board-select-mode-toggle").click();
			await firstCard.click();
			await secondCard.click();
			await expect(page.getByTestId("board-bulk-count")).toContainText("2");

			// Regression: the action must be reachable before any label exists.
			const labelsButton = page.getByTestId("board-bulk-labels");
			await expect(labelsButton).toBeEnabled();
			await labelsButton.click();

			const modal = page.getByTestId("board-bulk-labels-modal");
			await expect(modal).toBeVisible();

			// Create a brand-new label inline from the picker.
			const pickerInput = page.locator("#board-bulk-label-picker-input");
			await pickerInput.click();
			await pickerInput.pressSequentially(labelName);
			await page.getByTestId("board-bulk-label-picker-create").click();
			await expect(modal).toContainText(labelName);

			await page.getByTestId("board-bulk-labels-apply").click();
			await expect(page.getByTestId("toast-message-success")).toContainText(
				"Updated labels on 2 items",
			);
			await expect(modal).toHaveCount(0);

			// Reload and reopen: both items still carry the label.
			await page.reload();
			await expect(firstCard).toBeVisible({ timeout: 15_000 });
			await page.getByTestId("board-select-mode-toggle").click();
			await firstCard.click();
			await secondCard.click();
			await page.getByTestId("board-bulk-labels").click();
			await expect(page.getByTestId("board-bulk-labels-modal")).toContainText(
				labelName,
			);
		} finally {
			expect((await request.delete(`/api/v2/workspaces/${ws.id}`)).status()).toBe(204);
		}
	});

	test("removes a shared label from every selected item", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		await authenticateAdminRequest(request);

		const suffix = `bbr${Date.now()}`;
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const first = await createItemViaAPI(request, ws.id, {
			title: generateItem(ws.id, `${suffix}-a`).title,
		});
		const second = await createItemViaAPI(request, ws.id, {
			title: generateItem(ws.id, `${suffix}-b`).title,
		});
		const keep = await createLabelViaAPI(request, ws.id, `keep-${suffix}`);
		const drop = await createLabelViaAPI(request, ws.id, `drop-${suffix}`);
		await setItemLabelsViaAPI(request, first.id, [keep.id, drop.id]);
		await setItemLabelsViaAPI(request, second.id, [keep.id, drop.id]);

		try {
			await page.goto(`/workspaces/${ws.id}/board`);
			const firstCard = page.getByTestId(`board-item-${first.id}`);
			const secondCard = page.getByTestId(`board-item-${second.id}`);
			await expect(firstCard).toBeVisible({ timeout: 15_000 });
			await expect(secondCard).toBeVisible();

			await page.getByTestId("board-select-mode-toggle").click();
			await firstCard.click();
			await secondCard.click();
			await page.getByTestId("board-bulk-labels").click();

			const modal = page.getByTestId("board-bulk-labels-modal");
			await expect(modal).toContainText(keep.name);
			await expect(modal).toContainText(drop.name);

			// Deselect one label through the picker, then apply.
			await page.locator("#board-bulk-label-picker-input").click();
			await page.getByTestId(`board-bulk-label-picker-option-${drop.id}`).click();
			await page.getByTestId("board-bulk-labels-apply").click();
			await expect(page.getByTestId("toast-message-success")).toContainText(
				"Updated labels on 2 items",
			);

			// Reopen after reload: the kept label proves the picker loaded, so
			// the absent label is a real removal rather than a pending load.
			await page.reload();
			await expect(firstCard).toBeVisible({ timeout: 15_000 });
			await page.getByTestId("board-select-mode-toggle").click();
			await firstCard.click();
			await secondCard.click();
			await page.getByTestId("board-bulk-labels").click();
			const reopened = page.getByTestId("board-bulk-labels-modal");
			await expect(reopened).toContainText(keep.name);
			await expect(reopened).not.toContainText(drop.name);
		} finally {
			expect((await request.delete(`/api/v2/workspaces/${ws.id}`)).status()).toBe(204);
		}
	});
});
