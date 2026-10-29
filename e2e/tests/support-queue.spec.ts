import {
	authenticateAdminRequest,
	createItemViaAPI,
	createTeamViaAPI,
	createWorkspaceViaAPI,
	enableWorkspaceQueueViaAPI,
} from '../fixtures/api-helpers';
import type { Locator } from '@playwright/test';
import { expect, test } from '../fixtures/errors';

// Distance between the vertical centers of the leading icon and the trailing
// value text. A single-line cell keeps them aligned (near zero); a stacked
// cell separates them by roughly one line height.
async function iconTextCenterDelta(locator: Locator) {
	return locator.evaluate((element) => {
		const icon = element.querySelector('svg');
		const value = element.lastElementChild;
		if (!icon || !value) return Number.POSITIVE_INFINITY;
		const iconRect = icon.getBoundingClientRect();
		const valueRect = value.getBoundingClientRect();
		return Math.abs(
			iconRect.top + iconRect.height / 2 - (valueRect.top + valueRect.height / 2)
		);
	});
}

// Rendered line count of an element's text. Wrapped text produces one client
// rect per line; nowrap text produces exactly one.
async function valueLineCount(locator: Locator) {
	return locator.evaluate((element) => {
		const range = document.createRange();
		range.selectNodeContents(element);
		return range.getClientRects().length;
	});
}

const QUEUE_KEYS = [
	'unassigned',
	'team-owned',
	'assigned-to-me',
	'waiting',
	'overdue',
	'recently-updated',
	'sla-at-risk',
];

async function currentUserId(request: import('@playwright/test').APIRequestContext) {
	const response = await request.get(
		`${process.env.BASE_URL || 'http://localhost:8080'}/api/auth/me`,
	);
	expect(response.ok()).toBeTruthy();
	const session = await response.json();
	return session?.user?.id ?? session?.id ?? null;
}

// Looks up the persisted queue id after a UI creation so assertions can use
// the stable per-queue testid instead of visible text.
async function findQueueId(
	request: import('@playwright/test').APIRequestContext,
	workspaceKey: string,
	name: string,
): Promise<number> {
	const base = process.env.BASE_URL || 'http://localhost:8080';
	const response = await request.get(`${base}/api/v2/workspaces/${workspaceKey}/queues`);
	expect(response.ok()).toBeTruthy();
	const body = await response.json();
	const queues = Array.isArray(body) ? body : (body.data ?? []);
	const match = queues.find((queue: { name: string }) => queue.name === name);
	expect(match, `queue ${name} was persisted`).toBeTruthy();
	return match.id as number;
}

test.describe('support queue views', () => {
	test('renders the stable catalog, switches queues, and runs a bulk assignment', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const adminId = await currentUserId(request);
		const workspace = await createWorkspaceViaAPI(request, {
			name: `support-queue-${stamp}`,
			key: `SQ${stamp.slice(-6)}`.toUpperCase(),
			description: 'support queue e2e',
		});
		await enableWorkspaceQueueViaAPI(request, workspace.id);

		const team = await createTeamViaAPI(request, { name: `Queue Team ${stamp}` });

		// One unowned ticket (unassigned queue), one team-owned ticket
		// (team-owned queue), one overdue ticket (no assignee or team either,
		// so it also matches the unassigned queue), one assigned ticket.
		const orphan = await createItemViaAPI(request, workspace.id, {
			title: `Orphan ticket ${stamp}`,
		});
		await createItemViaAPI(request, workspace.id, {
			title: `Team ticket ${stamp}`,
			team_id: team.id,
		});
		const overdue = await createItemViaAPI(request, workspace.id, {
			title: `Overdue ticket ${stamp}`,
			due_date: '2026-01-01T00:00:00Z',
		} as never);
		await createItemViaAPI(request, workspace.id, {
			title: `Mine ticket ${stamp}`,
			assignee_id: adminId,
		});

		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page.getByTestId('support-queue-tabs')).toBeVisible({ timeout: 10_000 });

		// The queue table is built on the list view's shared table chrome.
		await expect(
			page.getByTestId('support-queue-table').getByTestId('list-header')
		).toBeVisible();

		// The catalog renders every queue in its stable order.
		const tabs = page.getByRole('tab');
		await expect(tabs).toHaveCount(QUEUE_KEYS.length);
		for (const [index, key] of QUEUE_KEYS.entries()) {
			await expect(tabs.nth(index)).toHaveAttribute('data-testid', `support-queue-tab-${key}`);
		}

		// The unassigned queue opens by default and shows the orphan plus the
		// overdue ticket, which has no assignee or team either.
		await expect(page.getByTestId('support-queue-tab-unassigned')).toHaveAttribute(
			'aria-selected',
			'true',
		);
		await expect(page.getByTestId(`support-queue-count-unassigned`)).toHaveText('2');
		await expect(page.getByTestId(`support-queue-row-${orphan.id}`)).toBeVisible();
		await expect(page.getByTestId(`support-queue-row-${overdue.id}`)).toBeVisible();
		await expect(page.getByTestId('support-queue-empty')).toBeHidden();

		// The team-owned queue shows only the team ticket.
		await page.getByTestId('support-queue-tab-team-owned').click();
		await expect(page.getByTestId('support-queue-tab-team-owned')).toHaveAttribute(
			'aria-selected',
			'true',
		);
		await expect(page.getByTestId('support-queue-count-team-owned')).toHaveText('1');

		// Bulk-safe action: assign the orphan to the current agent. The server
		// re-checks authorization per workspace; the queues refresh afterwards.
		await page.getByTestId('support-queue-tab-unassigned').click();
		await expect(page.getByTestId(`support-queue-row-${orphan.id}`)).toBeVisible();
		await page.getByTestId(`support-queue-item-checkbox-${orphan.id}`).check();
		await expect(page.getByTestId('support-queue-bulk-bar')).toBeVisible();
		await page.getByTestId('support-queue-bulk-assign-me').click();

		await expect(page.getByTestId('support-queue-count-unassigned')).toHaveText('1', {
			timeout: 10_000,
		});

		// The orphan moved into assigned-to-me; the overdue ticket stays put.
		await expect(page.getByTestId(`support-queue-row-${overdue.id}`)).toBeVisible();
		await expect(page.getByTestId(`support-queue-row-${orphan.id}`)).toBeHidden();
		await expect(page.getByTestId('support-queue-count-assigned-to-me')).toHaveText('2');
		await page.getByTestId('support-queue-tab-assigned-to-me').click();
		await expect(page.getByTestId(`support-queue-row-${orphan.id}`)).toBeVisible();

		// Deep links open the requested queue directly.
		await page.goto(`/workspaces/${workspace.id}/queue?queue=team-owned`);
		await expect(page.getByTestId('support-queue-tab-team-owned')).toHaveAttribute(
			'aria-selected',
			'true',
			{ timeout: 10_000 },
		);
	});

	test('bulk status transition and delete each run as one server request', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(request, {
			name: `support-queue-bulk-${stamp}`,
			key: `SQ${stamp.slice(-6)}`.toUpperCase(),
			description: 'support queue bulk transition/delete e2e',
		});
		await enableWorkspaceQueueViaAPI(request, workspace.id);

		const base = process.env.BASE_URL || 'http://localhost:8080';
		const statusesResponse = await request.get(`${base}/api/v2/statuses`);
		expect(statusesResponse.ok()).toBeTruthy();
		const statusesBody = await statusesResponse.json();
		const statuses = Array.isArray(statusesBody) ? statusesBody : (statusesBody.data ?? []);
		const done = statuses.find((status: { name: string }) => status.name === 'Done');
		expect(done, 'default Done status').toBeTruthy();

		const moveOne = await createItemViaAPI(request, workspace.id, {
			title: `Bulk move one ${stamp}`,
		});
		const moveTwo = await createItemViaAPI(request, workspace.id, {
			title: `Bulk move two ${stamp}`,
		});
		const deleteOne = await createItemViaAPI(request, workspace.id, {
			title: `Bulk delete one ${stamp}`,
		});
		const deleteTwo = await createItemViaAPI(request, workspace.id, {
			title: `Bulk delete two ${stamp}`,
		});

		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page.getByTestId('support-queue-tabs')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId(`support-queue-row-${moveOne.id}`)).toBeVisible();
		await expect(page.getByTestId(`support-queue-row-${moveTwo.id}`)).toBeVisible();
		await expect(page.getByTestId(`support-queue-row-${deleteOne.id}`)).toBeVisible();
		await expect(page.getByTestId(`support-queue-row-${deleteTwo.id}`)).toBeVisible();

		// Move both tickets to Done in a single batch request.
		await page.getByTestId(`support-queue-item-checkbox-${moveOne.id}`).check();
		await page.getByTestId(`support-queue-item-checkbox-${moveTwo.id}`).check();
		await expect(page.getByTestId('support-queue-bulk-bar')).toBeVisible();

		const transitionResponse = page.waitForResponse(
			(response) =>
				response.request().method() === 'POST' &&
				response.url().endsWith('/api/v2/items/bulk-transition'),
		);
		await page.locator('#support-queue-bulk-status').click();
		await page.locator(`#support-queue-bulk-status-option-${done.id}`).click();
		await page.getByTestId('support-queue-bulk-status-apply').click();
		expect((await transitionResponse).ok()).toBeTruthy();

		// Done is a completed status, so both leave the unassigned queue.
		await expect(page.getByTestId(`support-queue-row-${moveOne.id}`)).toBeHidden({
			timeout: 10_000,
		});
		await expect(page.getByTestId(`support-queue-row-${moveTwo.id}`)).toBeHidden();

		// Delete the two untouched tickets in a single batch request and confirm once.
		await page.getByTestId(`support-queue-item-checkbox-${deleteOne.id}`).check();
		await page.getByTestId(`support-queue-item-checkbox-${deleteTwo.id}`).check();

		const deleteResponse = page.waitForResponse(
			(response) =>
				response.request().method() === 'POST' &&
				response.url().endsWith('/api/v2/items/bulk-delete'),
		);
		await page.getByTestId('support-queue-bulk-delete').click();
		await page.getByTestId('dialog-confirm').click();
		expect((await deleteResponse).ok()).toBeTruthy();

		await expect(page.getByTestId(`support-queue-row-${deleteOne.id}`)).toBeHidden({
			timeout: 10_000,
		});
		await expect(page.getByTestId(`support-queue-row-${deleteTwo.id}`)).toBeHidden();
	});

	test('creates a queue from the builder, removes a built-in, and persists both', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(request, {
			name: `support-queue-manage-${stamp}`,
			key: `SQ${stamp.slice(-6)}`.toUpperCase(),
			description: 'support queue management e2e',
		});
		await enableWorkspaceQueueViaAPI(request, workspace.id);
		await createItemViaAPI(request, workspace.id, {
			title: `Escalation ${stamp}`,
		});

		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page.getByTestId('support-queue-tabs')).toBeVisible({ timeout: 10_000 });

		// Build the filter visually instead of typing CQL.
		await page.getByTestId('support-queue-add').click();
		await expect(page.getByTestId('support-queue-editor')).toBeVisible();
		await page.getByTestId('queue-editor-name').fill(`Escalations ${stamp}`);
		await page.getByTestId('queue-editor-builder-add').click();
		await page
			.getByTestId('queue-editor-builder-filter-0')
			.getByTestId('field-selector-trigger')
			.click();
		await page.getByTestId('field-option-title').click();
		await page.getByTestId('queue-editor-builder-filter-0-value').click();
		await page.getByTestId('queue-editor-builder-filter-0-value-input').fill(`Escalation ${stamp}`);
		await page.getByTestId('queue-editor-builder-filter-0-apply-value').click();
		// Synchronize on the create response before listing; the editor closes
		// and refreshes queues asynchronously after the POST settles.
		const queueCreated = page.waitForResponse(
			(response) =>
				response.request().method() === 'POST' &&
				new URL(response.url()).pathname.endsWith(
					`/api/v2/workspaces/${workspace.key}/queues`,
				) &&
				response.ok(),
		);
		await page.getByTestId('queue-editor-save').click();
		await queueCreated;

		const queueId = await findQueueId(request, workspace.key, `Escalations ${stamp}`);
		await expect(page.getByTestId(`support-queue-tab-${queueId}`)).toBeVisible({
			timeout: 10_000,
		});
		await page.getByTestId(`support-queue-tab-${queueId}`).click();
		await expect(page.getByTestId(`support-queue-count-${queueId}`)).toHaveText('1');

		// The custom queue survives a reload with its filter intact.
		await page.reload();
		await expect(page.getByTestId(`support-queue-tab-${queueId}`)).toBeVisible({
			timeout: 10_000,
		});
		await page.getByTestId(`support-queue-tab-${queueId}`).click();
		await expect(page.getByTestId(`support-queue-count-${queueId}`)).toHaveText('1');

		// Removing a built-in also persists: the waiting tab disappears for good.
		await page.getByTestId('support-queue-tab-waiting').click();
		await page.getByTestId('support-queue-menu-waiting').click();
		await page.getByTestId('support-queue-remove-builtin-waiting').click();
		await expect(page.getByTestId('support-queue-tab-waiting')).toBeHidden({
			timeout: 10_000,
		});
		await page.reload();
		await expect(page.getByTestId('support-queue-tabs')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId('support-queue-tab-waiting')).toBeHidden();
	});

	test('keeps the queue builder field menu unclipped by the dialog', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(request, {
			name: `support-queue-menu-${stamp}`,
			key: `SQ${stamp.slice(-6)}`.toUpperCase(),
			description: 'support queue menu e2e',
		});
		await enableWorkspaceQueueViaAPI(request, workspace.id);

		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page.getByTestId('support-queue-tabs')).toBeVisible({ timeout: 10_000 });
		await page.getByTestId('support-queue-add').click();
		await expect(page.getByTestId('support-queue-editor')).toBeVisible();
		await page.getByTestId('queue-editor-builder-add').click();
		await page
			.getByTestId('queue-editor-builder-filter-0')
			.getByTestId('field-selector-trigger')
			.click();
		const menu = page.getByTestId('field-selector-menu');
		await expect(menu).toBeVisible();

		// The portalled menu must escape the dialog's overflow clipping: a point
		// inside its lower edge still hit-tests to the menu itself.
		const hitInsideMenu = await menu.evaluate((element) => {
			const rect = element.getBoundingClientRect();
			const hit = document.elementFromPoint(
				rect.left + rect.width / 2,
				rect.bottom - 6,
			);
			return element.contains(hit);
		});
		expect(hitInsideMenu).toBe(true);
	});

	test('configures queue columns through the shared list column selector and persists them', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(request, {
			name: `support-queue-columns-${stamp}`,
			key: `SQ${stamp.slice(-6)}`.toUpperCase(),
			description: 'support queue columns e2e',
		});
		await enableWorkspaceQueueViaAPI(request, workspace.id);
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Column ticket ${stamp}`,
		});

		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page.getByTestId('support-queue-tabs')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId(`support-queue-row-${item.id}`)).toBeVisible();

		// The queue starts on the shared list defaults; assignee is not shown.
		await expect(page.getByTestId('support-queue-header-assignee')).toBeHidden();

		await page.getByTestId('queue-column-selector-trigger').click();
		await expect(page.getByTestId('column-selector-menu')).toBeVisible();
		await page.getByTestId('add-column-assignee').click();
		await page.getByTestId('add-column-due_date').click();
		await page.getByTestId('add-column-iteration').click();
		const saveResponse = page.waitForResponse(
			(response) =>
				response.request().method() === 'PUT' &&
				response.url().includes(`/api/v2/workspaces/${workspace.id}/board-configuration`),
		);
		await page.getByTestId('column-selector-apply').click();
		await saveResponse;

		// The shared list grid now renders the queue's Owner and Due Date columns.
		await expect(page.getByTestId('support-queue-header-assignee')).toBeVisible();
		await expect(page.getByTestId('support-queue-header-assignee')).toHaveText('Owner');
		await expect(page.getByTestId(`list-cell-assignee-${item.id}`)).toBeVisible();
		await expect(page.getByTestId('support-queue-header-due_date')).toBeVisible();

		// The due date cell keeps its icon and placeholder on one line instead of
		// stacking them (the editable date display previously used block layout).
		const queueRow = page.getByTestId(`support-queue-row-${item.id}`);
		await expect(queueRow.getByTestId('inline-date-display')).toHaveText('Set due date');
		expect(await iconTextCenterDelta(queueRow.getByTestId('inline-date-display'))).toBeLessThan(4);
		expect(await valueLineCount(queueRow.getByTestId('inline-date-value'))).toBe(1);
		await expect(queueRow.getByTestId('iteration-cell-display')).toHaveText('Select iteration');
		expect(await iconTextCenterDelta(queueRow.getByTestId('iteration-cell-display'))).toBeLessThan(4);
		expect(await valueLineCount(queueRow.getByTestId('iteration-cell-value'))).toBe(1);

		// Wide column sets scroll inside the canvas while the chrome (including
		// the Columns toggle) stays fixed to the viewport, matching the board.
		await page.setViewportSize({ width: 820, height: 720 });
		const canvas = page.getByTestId('support-queue-canvas');
		await expect
			.poll(() => canvas.evaluate((element) => element.scrollWidth > element.clientWidth))
			.toBe(true);
		const selector = page.getByTestId('queue-column-selector-trigger');
		const selectorBefore = await selector.boundingBox();
		await canvas.evaluate((element) => {
			element.scrollLeft = element.scrollWidth;
		});
		const selectorAfter = await selector.boundingBox();
		expect(selectorAfter?.x).toBe(selectorBefore?.x);
		const canvasBox = await canvas.boundingBox();
		expect((selectorAfter?.x ?? 0) + (selectorAfter?.width ?? 0)).toBeGreaterThan(
			(canvasBox?.x ?? 0) + (canvasBox?.width ?? 0) - 48
		);

		// The same configuration and cell renderer back the list view.
		await page.goto(`/workspaces/${workspace.id}/list`);
		await expect(page.getByTestId(`workspace-item-row-${item.id}`)).toBeVisible({
			timeout: 10_000,
		});
		await expect(page.getByTestId(`list-cell-assignee-${item.id}`)).toBeVisible();
		const listRow = page.getByTestId(`workspace-item-row-${item.id}`);
		await expect(listRow.getByTestId('inline-date-display')).toHaveText('Set due date');
		expect(await iconTextCenterDelta(listRow.getByTestId('inline-date-display'))).toBeLessThan(4);
		expect(await valueLineCount(listRow.getByTestId('inline-date-value'))).toBe(1);
		await expect(listRow.getByTestId('iteration-cell-display')).toHaveText('Select iteration');
		expect(await iconTextCenterDelta(listRow.getByTestId('iteration-cell-display'))).toBeLessThan(4);
		expect(await valueLineCount(listRow.getByTestId('iteration-cell-value'))).toBe(1);

		// Reloading the queue keeps the configured column.
		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page.getByTestId('support-queue-header-assignee')).toBeVisible({
			timeout: 10_000,
		});
	});
});
