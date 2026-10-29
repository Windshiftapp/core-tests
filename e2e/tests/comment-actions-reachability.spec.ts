import { createItemViaAPI } from '../fixtures/api-helpers';
import {
	expect,
	test,
	type APIRequestContext,
	type Browser,
	type Page,
} from '../fixtures/context-path';
import {
	createGatedWorkspaceWithSessions,
	disposeGatedWorkspace,
	type GatedWorkspace,
} from '../fixtures/role-sessions';
import { generateItem } from '../fixtures/test-data';

/**
 * GH #317 — hover-only comment actions must stay reachable without hover.
 *
 * Tailwind v4 wraps `hover:`/`group-hover:` in `@media (hover: hover)`, so on
 * touch screens and other devices that report no hover the edit/delete
 * actions stayed at opacity 0 and were unreachable. They now also reveal on
 * `group-focus-within` and on `(pointer: coarse)`.
 *
 * Opacity is the observable: Playwright treats an opacity:0 element as
 * visible, so `toBeVisible()` cannot tell the two states apart.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function createCommentedItem(
	request: APIRequestContext,
	browser: Browser,
	suffix: string,
): Promise<{ fixture: GatedWorkspace; itemId: number }> {
	const fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
		{ label: 'admin', role: 'Administrator' },
	]);
	const admin = fixture.sessionsByLabel.admin;
	const itemData = generateItem(fixture.workspaceId, suffix);
	const item = await createItemViaAPI(request, fixture.workspaceId, {
		title: itemData.title,
	});
	const comment = await admin.request.post(`/api/v2/items/${item.id}/comments`, {
		headers: SEC_FETCH,
		data: { content: `comment ${suffix}`, is_private: false },
	});
	expect(comment.status(), await comment.text()).toBe(201);
	return { fixture, itemId: item.id };
}

/** Emulate a touch-only device, which reports (hover: none)/(pointer: coarse). */
async function emulateNoHover(page: Page): Promise<void> {
	const client = await page.context().newCDPSession(page);
	await client.send('Emulation.setTouchEmulationEnabled', { enabled: true });
}

test.describe('comment action reachability', () => {
	test('actions are visible when the device reports no hover (GH #317)', async ({
		request,
		browser,
		browserName,
	}) => {
		test.skip(
			browserName !== 'chromium',
			'Emulation.setEmulatedMedia is Chromium-only',
		);
		test.setTimeout(90_000);
		const suffix = `cahr${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			const created = await createCommentedItem(request, browser, suffix);
			fixture = created.fixture;
			const admin = fixture.sessionsByLabel.admin;

			await admin.page.goto(
				`/workspaces/${fixture.workspaceId}/items/${created.itemId}`,
			);
			// Enable after navigation so the touch media features are live before
			// the comment actions render.
			await emulateNoHover(admin.page);
			expect(
				await admin.page.evaluate(() => ({
					hover: matchMedia('(hover: none)').matches,
					pointer: matchMedia('(pointer: coarse)').matches,
				})),
			).toEqual({ hover: true, pointer: true });
			await expect(admin.page.getByTestId('item-detail-ready')).toBeVisible({
				timeout: 15_000,
			});
			const actions = admin.page
				.getByTestId('comment-item')
				.first()
				.getByTestId('comment-actions');
			await expect(actions).toBeVisible({ timeout: 15_000 });
			await expect(actions).toHaveCSS('opacity', '1');
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});

	test('actions reveal on keyboard focus (GH #317)', async ({ request, browser }) => {
		test.setTimeout(90_000);
		const suffix = `cahk${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			const created = await createCommentedItem(request, browser, suffix);
			fixture = created.fixture;
			const admin = fixture.sessionsByLabel.admin;

			await admin.page.goto(
				`/workspaces/${fixture.workspaceId}/items/${created.itemId}`,
			);
			await expect(admin.page.getByTestId('item-detail-ready')).toBeVisible({
				timeout: 15_000,
			});
			const row = admin.page.getByTestId('comment-item').first();
			const actions = row.getByTestId('comment-actions');
			await expect(actions).toHaveCSS('opacity', '0');

			await row.getByTestId('comment-edit').focus();
			await expect(actions).toHaveCSS('opacity', '1');
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});
});
