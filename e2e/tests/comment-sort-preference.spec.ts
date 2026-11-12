import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { preferenceSave } from '../helpers/preferences';
import {
	assignWorkspaceRole,
	createWorkspaceUser,
	openSession,
	type SessionUser,
} from '../fixtures/role-sessions';
import { generateWorkspace } from '../fixtures/test-data';

/**
 * The comment sort toggle writes a per-user preference. A dedicated session
 * user keeps the write isolated from the shared admin session that other
 * comment specs run as, so parallel workers cannot flip each other's order.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface CommentSortFixture {
	workspaceId: number;
	itemId: number;
	userIds: number[];
	session: SessionUser;
}

async function createCommentSortFixture(
	request: import('@playwright/test').APIRequestContext,
	browser: import('@playwright/test').Browser,
	suffix: string,
): Promise<CommentSortFixture> {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(suffix),
	);
	const userIds: number[] = [];

	// Gate the workspace so access comes only from the explicit role.
	const gateUser = await createWorkspaceUser(request, `${suffix}-gate`);
	userIds.push(gateUser.id);
	await assignWorkspaceRole(request, gateUser.id, workspace.id, 'Viewer');

	const user = await createWorkspaceUser(request, suffix);
	userIds.push(user.id);
	await assignWorkspaceRole(request, user.id, workspace.id, 'Editor');
	const session = await openSession(browser, user);

	const item = await createItemViaAPI(request, workspace.id, {
		title: `Comment sort ${suffix}`,
	});
	for (let index = 1; index <= 3; index += 1) {
		const response = await request.post(`/api/v2/items/${item.id}/comments`, {
			headers: SEC_FETCH,
			data: { content: `sort-comment-${index}` },
		});
		expect(response.ok(), `create comment ${index}: ${response.status()}`).toBeTruthy();
		await response.dispose();
	}

	return { workspaceId: workspace.id, itemId: item.id, userIds, session };
}

async function disposeCommentSortFixture(
	request: import('@playwright/test').APIRequestContext,
	fixture: CommentSortFixture,
): Promise<void> {
	await fixture.session.context.close();
	const deleted = await request.delete(`/api/v2/workspaces/${fixture.workspaceId}`, {
		headers: SEC_FETCH,
	});
	expect(deleted.status(), `delete workspace ${fixture.workspaceId}`).toBe(204);
	for (const userId of fixture.userIds) {
		const resp = await request.delete(`/api/users/${userId}`, {
			headers: SEC_FETCH,
		});
		expect(resp.ok(), `delete user ${userId}`).toBeTruthy();
	}
}

test.describe('comment sort preference', () => {
	test('persists the toggled order across a reload', async ({ request, browser }) => {
		test.setTimeout(90_000);
		const suffix = `csp${Date.now()}`;
		let fixture: CommentSortFixture | undefined;
		try {
			fixture = await createCommentSortFixture(request, browser, suffix);
			const { session, workspaceId, itemId } = fixture;
			const page = session.page;

			await page.goto(`/workspaces/${workspaceId}/items/${itemId}`);
			await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 15_000 });

			const section = page.getByTestId('comments-section');
			const rows = section.getByTestId('comment-item');
			await expect(rows).toHaveCount(3);
			await expect(rows.first()).toContainText('sort-comment-1');
			await expect(rows.last()).toContainText('sort-comment-3');

			const saved = preferenceSave(page);
			await section.getByTestId('comments-sort-toggle').click();
			await saved;
			await expect(rows.first()).toContainText('sort-comment-3');
			await expect(rows.last()).toContainText('sort-comment-1');

			// The preference is loaded with the shell settings, so a fresh page
			// load must render newest-first without another toggle.
			await page.reload();
			await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 15_000 });
			await expect(rows).toHaveCount(3);
			await expect(rows.first()).toContainText('sort-comment-3');
			await expect(rows.last()).toContainText('sort-comment-1');
		} finally {
			if (fixture) await disposeCommentSortFixture(request, fixture);
		}
	});
});
