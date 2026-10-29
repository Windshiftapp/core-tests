import { createItemViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import {
	createGatedWorkspaceWithSessions,
	disposeGatedWorkspace,
	type GatedWorkspace,
} from '../fixtures/role-sessions';
import { generateItem } from '../fixtures/test-data';

/**
 * Mobile item surface vs item.edit / item.create.
 *
 * The /m/ shell must apply the same permission gates as the desktop
 * surfaces: the item-detail edit button and status picker are item.edit
 * write affordances (desktop parity: ItemDetail.svelte / ItemDetailSidebar;
 * WI-1439), and the create page's workspace picker only offers workspaces
 * where the user holds item.create (desktop parity: the create modal,
 * WI-1438/1440). The backend is the contract: item-scoped denials are 404
 * (PATCH /items/{id} and POST /items/{id}/transition), not 403.
 *
 * The mobile Pages tab is deliberately NOT asserted: every user holds all
 * permissions in their personal workspace, so a global Pages tab is
 * legitimate for every role.
 *
 * Assertions target the uniquely-named gated workspace only. The
 * everyone-Viewer fallback makes every concurrently-created ungated
 * workspace visible to fixture users, so total option counts are not a
 * stable contract here.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const MOBILE_VIEWPORT = { width: 390, height: 844 };

async function getWorkspace(
	request: import('@playwright/test').APIRequestContext,
	workspaceId: number,
): Promise<{ key: string; name: string }> {
	const resp = await request.get(`/api/v2/workspaces/${workspaceId}`, {
		headers: SEC_FETCH,
	});
	expect(resp.ok(), `get workspace: ${resp.status()}`).toBeTruthy();
	const data = (await resp.json()).data;
	return { key: data.key as string, name: data.name as string };
}

/**
 * Option labels of the create page's workspace select, read from the
 * testid-rooted element: the picker's observable content, without locating
 * individual option elements.
 */
async function workspaceOptionLabels(page: import('@playwright/test').Page): Promise<string[]> {
	const select = page.getByTestId('create-workspace');
	await expect(select).toBeVisible({ timeout: 15_000 });
	return select.evaluate((el) =>
		Array.from((el as HTMLSelectElement).options).map((o) => o.textContent ?? ''),
	);
}

/** Open the mobile item detail and wait for the item data to render. */
async function openMobileItem(page: import('@playwright/test').Page, itemId: number, title: string) {
	await page.setViewportSize(MOBILE_VIEWPORT);
	await page.goto(`/m/items/${itemId}`);
	await expect(page.getByTestId('detail-title')).toHaveText(title, {
		timeout: 15_000,
	});
}

/**
 * Wait for the create page's two data sources to settle: the workspace
 * directory (which caches only its first server page, WI-1442) and the user's
 * permission profile (the picker filters the directory by item.create).
 * Option assertions that run before both responses have been applied by the
 * page would race the stores, so every picker test waits here first.
 */
async function waitForCreatePickerData(page: import('@playwright/test').Page) {
	const directoryPage = page.waitForResponse(async (r) => {
		if (
			r.request().method() !== 'GET' ||
			!/\/api\/v2\/workspaces(\?|$)/.test(r.url()) ||
			!r.ok()
		) {
			return false;
		}
		const body = (await r.json()) as {
			pagination?: { page?: number };
		};
		return !body.pagination || (body.pagination.page ?? 1) === 1;
	});
	const permissionProfile = page.waitForResponse(
		(r) =>
			r.request().method() === 'GET' &&
			/\/api\/users\/\d+\/permissions$/.test(new URL(r.url()).pathname) &&
			r.ok(),
	);
	await Promise.all([directoryPage, permissionProfile]);
}

test.describe('mobile item surface vs item.edit / item.create', () => {
	test('viewer without item.edit: no edit affordance on mobile item detail and PATCH is 404', async ({
		request,
		browser,
	}) => {
		test.fail(true, 'MobileItemDetail renders the edit button without item.edit (WI-1439)');
		test.setTimeout(90_000);
		const suffix = `mipv${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
				{ label: 'viewer', role: 'Viewer' },
			]);
			const { workspaceId, sessionsByLabel } = fixture;
			const viewer = sessionsByLabel.viewer;

			const itemData = generateItem(workspaceId, suffix);
			const item = await createItemViaAPI(request, workspaceId, {
				title: itemData.title,
			});

			await openMobileItem(viewer.page, item.id, itemData.title);

			// The edit button is an item.edit write affordance; a Viewer must
			// not be offered it (desktop hides the same affordance).
			await expect(viewer.page.getByTestId('detail-edit')).toHaveCount(0);

			// Same contract server-side: the editor save is a PATCH, denied
			// with 404 for item-scoped permission failures.
			const denied = await viewer.request.patch(`/api/v2/items/${item.id}`, {
				headers: { ...SEC_FETCH, 'Content-Type': 'application/merge-patch+json' },
				data: { title: `${itemData.title} patched` },
			});
			expect(denied.status(), await denied.text()).toBe(404);

			const adminGet = await request.get(`/api/v2/items/${item.id}`, {
				headers: SEC_FETCH,
			});
			expect(adminGet.ok()).toBeTruthy();
			expect((await adminGet.json()).data.title).toBe(itemData.title);
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});

	test('viewer without item.edit: the mobile status picker is not offered', async ({
		request,
		browser,
	}) => {
		test.fail(true, 'MobileItemDetail renders the status picker without item.edit (WI-1439)');
		test.setTimeout(90_000);
		const suffix = `mips${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
				{ label: 'viewer', role: 'Viewer' },
			]);
			const { workspaceId, sessionsByLabel } = fixture;
			const viewer = sessionsByLabel.viewer;

			const itemData = generateItem(workspaceId, suffix);
			const item = await createItemViaAPI(request, workspaceId, {
				title: itemData.title,
			});

			await openMobileItem(viewer.page, item.id, itemData.title);

			// The status trigger opens the status-change sheet and POSTs a
			// transition (item.edit; the 404 denial is pinned in
			// board-viewer-permissions.spec.ts). A Viewer must not be offered it.
			await expect(viewer.page.getByTestId('status-picker-trigger')).toHaveCount(0);
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});

	test('editor with item.edit: the mobile edit affordance saves and persists', async ({
		request,
		browser,
	}) => {
		test.setTimeout(90_000);
		const suffix = `mipe${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
				{ label: 'editor', role: 'Editor' },
			]);
			const { workspaceId, sessionsByLabel } = fixture;
			const editor = sessionsByLabel.editor;

			const itemData = generateItem(workspaceId, suffix);
			const item = await createItemViaAPI(request, workspaceId, {
				title: itemData.title,
			});

			await openMobileItem(editor.page, item.id, itemData.title);

			// The Editor holds item.edit: the affordance is present and the
			// editor round-trips a title change.
			await editor.page.getByTestId('detail-edit').click();
			await expect(editor.page).toHaveURL(new RegExp(`/m/items/${item.id}/edit$`));
			const titleInput = editor.page.getByTestId('item-edit-title');
			await expect(titleInput).toHaveValue(itemData.title);
			const newTitle = `${itemData.title} via mobile`;
			await titleInput.fill(newTitle);
			await editor.page.getByTestId('editor-save').click();

			await expect(editor.page).toHaveURL(new RegExp(`/m/items/${item.id}$`));
			await expect(editor.page.getByTestId('detail-title')).toHaveText(newTitle);

			// Prove persistence, not just post-save state.
			await editor.page.reload();
			await expect(editor.page.getByTestId('detail-title')).toHaveText(newTitle);
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});

	test('viewer without item.create: the mobile create picker does not offer the gated workspace', async ({
		request,
		browser,
	}) => {
		test.setTimeout(90_000);
		const suffix = `micv${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
				{ label: 'viewer', role: 'Viewer' },
			]);
			const { workspaceId, sessionsByLabel } = fixture;
			const viewer = sessionsByLabel.viewer;
			const workspace = await getWorkspace(request, workspaceId);

			await viewer.page.setViewportSize(MOBILE_VIEWPORT);
			// Register the response waits before navigating so a fast load cannot
			// settle the stores before the listeners attach.
			const pickerData = waitForCreatePickerData(viewer.page);
			await viewer.page.goto('/m/new');
			await pickerData;

			// The gated workspace must not be offered as a creation target.
			const optionLabels = await workspaceOptionLabels(viewer.page);
			expect(optionLabels).not.toContain(workspace.name);
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});

	test('editor with item.create: the mobile create picker offers the gated workspace', async ({
		request,
		browser,
	}) => {
		test.setTimeout(90_000);
		const suffix = `mice${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
				{ label: 'editor', role: 'Editor' },
			]);
			const { workspaceId, sessionsByLabel } = fixture;
			const editor = sessionsByLabel.editor;
			const workspace = await getWorkspace(request, workspaceId);

			await editor.page.setViewportSize(MOBILE_VIEWPORT);
			// Register the response waits before navigating so a fast load cannot
			// settle the stores before the listeners attach.
			const pickerData = waitForCreatePickerData(editor.page);
			await editor.page.goto('/m/new');
			await pickerData;
			const optionLabels = await workspaceOptionLabels(editor.page);

			// The Editor holds item.create: the gated workspace is offered
			// exactly once in the picker.
			expect(optionLabels.filter((label) => label === workspace.name)).toHaveLength(1);
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});
});
