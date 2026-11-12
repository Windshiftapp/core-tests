import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import {
	type APIRequestContext,
	expect,
	type Locator,
	type Page,
	test,
} from "../fixtures/context-path";
import { generateItem, generateWorkspace } from "../fixtures/test-data";

/**
 * Board moves that carry a transition screen.
 *
 * A status drop between columns must open the screen modal before moving, and
 * the modal must seed the displayed fields from the item so the user does not
 * retype the title or description. The modal is portaled above the board so a
 * portaled dialog cannot hide it.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

const MARKDOWN_DESCRIPTION = "### Steps to reproduce\n\n* Step 1";

interface Status {
	id: number;
	name: string;
}

interface Workflow {
	id: number;
}

interface Transition {
	id: number;
	from?: { id: number };
	to: { id: number };
}

function unwrap<T>(body: unknown): T {
	const envelope = body as { data?: T } | undefined;
	return (envelope?.data ?? body) as T;
}

async function getItemStatus(
	request: APIRequestContext,
	itemId: number,
): Promise<number | null> {
	const resp = await request.get(`/api/v2/items/${itemId}`, { headers: SEC_FETCH });
	const body = unwrap<{ status_id: number | null }>(await resp.json());
	return body.status_id;
}

async function listWorkspaceStatuses(
	request: APIRequestContext,
	workspaceId: number,
): Promise<Status[]> {
	const resp = await request.get(`/api/v2/workspaces/${workspaceId}/statuses`, {
		headers: SEC_FETCH,
	});
	expect(resp.ok(), `list statuses failed (${resp.status()})`).toBeTruthy();
	return unwrap<Status[]>(await resp.json());
}

async function listWorkspaceWorkflows(
	request: APIRequestContext,
	workspaceId: number,
): Promise<Workflow[]> {
	const resp = await request.get(`/api/v2/workspaces/${workspaceId}/workflows`, {
		headers: SEC_FETCH,
	});
	expect(resp.ok(), `list workflows failed (${resp.status()})`).toBeTruthy();
	return unwrap<Workflow[]>(await resp.json());
}

async function listWorkflowTransitions(
	request: APIRequestContext,
	workflowId: number,
): Promise<Transition[]> {
	const resp = await request.get(`/api/v2/workflows/${workflowId}/transitions`, {
		headers: SEC_FETCH,
	});
	expect(resp.ok(), `list transitions failed (${resp.status()})`).toBeTruthy();
	return unwrap<Transition[]>(await resp.json());
}

async function bindTransitionScreen(
	request: APIRequestContext,
	transitionId: number,
	screenId: number,
): Promise<void> {
	const resp = await request.put(`/api/v2/transitions/${transitionId}/screen`, {
		headers: SEC_FETCH,
		data: { screen_id: screenId },
	});
	expect(
		resp.ok(),
		`bind transition screen failed (${resp.status()}): ${await resp.text()}`,
	).toBeTruthy();
}

async function createTransitionScreen(
	request: APIRequestContext,
	name: string,
): Promise<{ id: number }> {
	const createResp = await request.post("/api/v2/screens", {
		headers: SEC_FETCH,
		data: { name, description: "board transition screen e2e" },
	});
	expect(
		createResp.ok(),
		`create screen failed (${createResp.status()}): ${await createResp.text()}`,
	).toBeTruthy();
	const screen = unwrap<{ id: number }>(await createResp.json());

	const fieldsResp = await request.put(`/api/v2/screens/${screen.id}/fields`, {
		headers: SEC_FETCH,
		data: {
			fields: [
				{
					field_type: "system",
					field_identifier: "title",
					display_order: 0,
					is_required: true,
					field_width: "full",
				},
				{
					field_type: "system",
					field_identifier: "description",
					display_order: 1,
					is_required: false,
					field_width: "full",
				},
			],
		},
	});
	expect(
		fieldsResp.ok(),
		`set screen fields failed (${fieldsResp.status()}): ${await fieldsResp.text()}`,
	).toBeTruthy();
	return screen;
}

async function expectMarkdownDescription(modal: Locator) {
	const description = modal.getByTestId("transition-field-description");
	await expect(description).toBeVisible({ timeout: 15_000 });
	await expect(description).toHaveAttribute("data-ready", "true", { timeout: 15_000 });

	const rendered = await description.evaluate((node) => ({
		heading: node.querySelector("h3")?.textContent ?? null,
		listItems: Array.from(node.querySelectorAll("li")).map((li) => li.textContent?.trim()),
		raw: node.textContent ?? "",
	}));
	expect(rendered.heading).toBe("Steps to reproduce");
	expect(rendered.listItems).toContain("Step 1");
	// The raw markers must be gone, proving the editor parsed the markdown.
	expect(rendered.raw).not.toContain("###");
	expect(rendered.raw).not.toContain("* Step 1");
}

async function dragCardToColumn(page: Page, card: Locator, target: Locator) {
	const cardBox = await card.boundingBox();
	const targetBox = await target.boundingBox();
	if (!cardBox || !targetBox) {
		throw new Error("Board drag source and target must both be visible");
	}
	const startX = cardBox.x + cardBox.width / 2;
	const startY = cardBox.y + cardBox.height / 2;
	const targetX = targetBox.x + targetBox.width / 2;
	const targetY = targetBox.y + Math.min(targetBox.height / 2, 160);
	await page.mouse.move(startX, startY);
	await page.mouse.down();
	await page.mouse.move(startX + 12, startY + 12);
	await page.mouse.move(targetX, targetY, { steps: 12 });
	await page.mouse.up();
}

test("item detail modal status change opens the transition screen above the modal", async ({
	page,
	request,
}) => {
	test.setTimeout(90_000);
	await authenticateAdminRequest(request);

	const suffix = `its${Date.now()}`;
	const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
	const statuses = await listWorkspaceStatuses(request, ws.id);
	expect(statuses.length).toBeGreaterThanOrEqual(2);

	const [workflow] = await listWorkspaceWorkflows(request, ws.id);
	expect(workflow, "fresh workspace has no workflow").toBeDefined();
	const transitions = await listWorkflowTransitions(request, workflow.id);

	const fromStatus = statuses[0];
	const toStatus = statuses[1];
	const transition = transitions.find(
		(candidate) => candidate.from?.id === fromStatus.id && candidate.to.id === toStatus.id,
	);
	expect(
		transition,
		`no transition ${fromStatus.id} -> ${toStatus.id} in workflow ${workflow.id}`,
	).toBeDefined();

	const screen = await createTransitionScreen(request, `Item modal screen ${suffix}`);
	await bindTransitionScreen(request, transition!.id, screen.id);

	const itemData = generateItem(ws.id, suffix);
	const item = await createItemViaAPI(request, ws.id, {
		title: itemData.title,
		description: MARKDOWN_DESCRIPTION,
	});

	await page.goto(`/workspaces/${ws.id}/board`);
	await expect(page.getByTestId("board-view")).toBeVisible({ timeout: 15_000 });
	await page.getByTestId(`board-item-${item.id}`).click();

	const dialog = page.getByTestId("item-detail-modal");
	await expect(dialog).toBeVisible({ timeout: 10_000 });

	// The status picker lives in the modal sidebar; its options are keyed by the
	// target status id so the selection is stable across translations.
	await dialog.getByTestId("status-field").click();
	await page.getByTestId(`item-detail-status-option-${toStatus.id}`).click();

	const modal = page.getByTestId("transition-screen-modal");
	await expect(modal).toBeVisible({ timeout: 10_000 });
	await expect(modal.getByTestId("transition-field-title")).toHaveValue(itemData.title);
	await expectMarkdownDescription(modal);

	await modal.getByTestId("dialog-confirm").click();

	await expect
		.poll(
			async () => {
				const resp = await request.get(`/api/v2/items/${item.id}`, { headers: SEC_FETCH });
				const body = unwrap<{ status_id: number | null }>(await resp.json());
				return body.status_id;
			},
			{ message: `item ${item.id} did not transition to ${toStatus.id}`, timeout: 15_000 },
		)
		.toBe(toStatus.id);
});

test("keyboard shortcuts submit and cancel the transition screen", async ({
	page,
	request,
}) => {
	test.setTimeout(90_000);
	await authenticateAdminRequest(request);

	const suffix = `tsk${Date.now()}`;
	const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
	const statuses = await listWorkspaceStatuses(request, ws.id);
	expect(statuses.length).toBeGreaterThanOrEqual(2);

	const [workflow] = await listWorkspaceWorkflows(request, ws.id);
	expect(workflow, "fresh workspace has no workflow").toBeDefined();
	const transitions = await listWorkflowTransitions(request, workflow.id);

	const fromStatus = statuses[0];
	const toStatus = statuses[1];
	const transition = transitions.find(
		(candidate) => candidate.from?.id === fromStatus.id && candidate.to.id === toStatus.id,
	);
	expect(
		transition,
		`no transition ${fromStatus.id} -> ${toStatus.id} in workflow ${workflow.id}`,
	).toBeDefined();

	const screen = await createTransitionScreen(request, `Keyboard screen ${suffix}`);
	await bindTransitionScreen(request, transition!.id, screen.id);

	const itemData = generateItem(ws.id, suffix);
	const item = await createItemViaAPI(request, ws.id, {
		title: itemData.title,
		description: MARKDOWN_DESCRIPTION,
	});

	await page.goto(`/workspaces/${ws.id}/board`);
	await expect(page.getByTestId("board-view")).toBeVisible({ timeout: 15_000 });
	await page.getByTestId(`board-item-${item.id}`).click();

	const dialog = page.getByTestId("item-detail-modal");
	await expect(dialog).toBeVisible({ timeout: 10_000 });

	const openScreen = async () => {
		await dialog.getByTestId("status-field").click();
		await page.getByTestId(`item-detail-status-option-${toStatus.id}`).click();
		const modal = page.getByTestId("transition-screen-modal");
		await expect(modal).toBeVisible({ timeout: 10_000 });
		return modal;
	};

	// Escape cancels the screen without moving the item, and the footer exposes
	// the same shortcut hints as every other modal.
	let modal = await openScreen();
	await expect(modal.getByTestId("dialog-confirm")).toContainText("↵");
	await page.keyboard.press("Escape");
	await expect(modal).toBeHidden({ timeout: 10_000 });
	expect(await getItemStatus(request, item.id)).toBe(fromStatus.id);

	// The submit shortcut moves the item.
	modal = await openScreen();
	await page.keyboard.press("Control+Enter");
	await expect(modal).toBeHidden({ timeout: 10_000 });
	await expect
		.poll(async () => getItemStatus(request, item.id), {
			message: `item ${item.id} did not transition to ${toStatus.id}`,
			timeout: 15_000,
		})
		.toBe(toStatus.id);
});

test("board drop opens the transition screen pre-filled from the item", async ({
	page,
	request,
}) => {
	test.setTimeout(90_000);
	await authenticateAdminRequest(request);

	const suffix = `bts${Date.now()}`;
	const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
	const statuses = await listWorkspaceStatuses(request, ws.id);
	expect(statuses.length).toBeGreaterThanOrEqual(2);

	const [workflow] = await listWorkspaceWorkflows(request, ws.id);
	expect(workflow, "fresh workspace has no workflow").toBeDefined();
	const transitions = await listWorkflowTransitions(request, workflow.id);

	const fromStatus = statuses[0];
	const toStatus = statuses[1];
	const transition = transitions.find(
		(candidate) => candidate.from?.id === fromStatus.id && candidate.to.id === toStatus.id,
	);
	expect(
		transition,
		`no transition ${fromStatus.id} -> ${toStatus.id} in workflow ${workflow.id}`,
	).toBeDefined();

	const screen = await createTransitionScreen(request, `Board screen ${suffix}`);
	await bindTransitionScreen(request, transition!.id, screen.id);

	const itemData = generateItem(ws.id, suffix);
	const item = await createItemViaAPI(request, ws.id, {
		title: itemData.title,
		description: MARKDOWN_DESCRIPTION,
	});

	await page.goto(`/workspaces/${ws.id}/board`);
	await expect(page.getByTestId("board-view")).toBeVisible({ timeout: 15_000 });

	const card = page.getByTestId(`board-item-${item.id}`);
	const targetColumn = page.locator(`#board-column-status-${toStatus.id}`);
	await expect(card).toHaveAttribute("draggable", "true", { timeout: 5_000 });
	await expect(targetColumn).toBeVisible();

	await dragCardToColumn(page, card, targetColumn);

	// The drop must not move the item silently; it opens the screen instead.
	const modal = page.getByTestId("transition-screen-modal");
	await expect(modal).toBeVisible({ timeout: 10_000 });
	await expect(modal.getByTestId("transition-field-title")).toHaveValue(itemData.title);
	await expectMarkdownDescription(modal);

	await modal.getByTestId("dialog-confirm").click();

	await expect
		.poll(
			async () => {
				const resp = await request.get(`/api/v2/items/${item.id}`, { headers: SEC_FETCH });
				const body = unwrap<{ status_id: number | null }>(await resp.json());
				return body.status_id;
			},
			{ message: `item ${item.id} did not transition to ${toStatus.id}`, timeout: 15_000 },
		)
		.toBe(toStatus.id);
});
