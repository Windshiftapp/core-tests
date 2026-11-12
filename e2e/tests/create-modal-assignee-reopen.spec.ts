import { expect, test, type Page } from '../fixtures/context-path';
import {
	createGatedWorkspaceWithSessions,
	disposeGatedWorkspace,
	type GatedWorkspace,
} from '../fixtures/role-sessions';

/**
 * GH #420: the create modal's assignee picker went empty when the modal was
 * reopened without a page reload.
 *
 * The form store keeps its loaded screen fields, so on reopen the assignee
 * field rendered before the form store had reapplied the workspace scope.
 * The picker mounted with a null workspaceId, loaded the global /users
 * directory, and never reloaded once the workspace arrived. Editors hold
 * users:read but not the global user.list permission, so the fallback
 * request was denied and the list came back empty.
 *
 * The picker must resolve its roster from the workspace-scoped
 * assignable-users endpoint on every open, including the first open after a
 * close.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function openBoard(page: Page, workspaceId: number) {
	await page.addInitScript(
		([id]) => {
			window.localStorage.setItem('vertex_create_modal_workspace', String(id));
		},
		[workspaceId],
	);
	const workspacesLoaded = page.waitForResponse(
		(r) =>
			r.request().method() === 'GET' &&
			/\/api\/v2\/workspaces(\?|$)/.test(r.url()) &&
			r.ok(),
	);
	await page.goto(`/workspaces/${workspaceId}/board`);
	await expect(page.getByTestId('board-view')).toBeVisible({ timeout: 15_000 });
	await workspacesLoaded;
}

async function openCreateModal(page: Page) {
	await page.locator('#global-create-button').click();
	await expect(page.getByTestId('create-modal')).toBeVisible();
}

test.describe('create modal assignee picker (GH #420)', () => {
	test('offers workspace members on reopen without a reload', async ({ request, browser }) => {
		test.setTimeout(90_000);
		const suffix = `cma${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		try {
			fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
				{ label: 'editor', role: 'Editor' },
			]);
			const { workspaceId, sessionsByLabel } = fixture;
			const editor = sessionsByLabel.editor;
			const page = editor.page;

			await openBoard(page, workspaceId);

			// First open.
			await openCreateModal(page);
			await page.getByTestId('create-assignee-chip').click();
			await expect(page.getByTestId(`user-picker-option-${editor.id}`)).toBeVisible();
			await page.getByTestId('create-modal-close').click();
			await expect(page.getByTestId('create-modal')).toBeHidden();

			// Reopen without reloading the page.
			await openCreateModal(page);
			await page.getByTestId('create-assignee-chip').click();
			await expect(page.getByTestId(`user-picker-option-${editor.id}`)).toBeVisible();
		} finally {
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});
});
