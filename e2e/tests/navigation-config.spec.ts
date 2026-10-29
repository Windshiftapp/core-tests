import type { APIRequestContext, BrowserContext } from '@playwright/test';
import {
	authenticateAdminRequest,
	createCollectionViaAPI,
	createUserViaAPI,
	createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateUser, generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function workspaceRoleId(request: APIRequestContext, name: string): Promise<number> {
	const response = await request.get('/api/workspace-roles', {
		headers: SEC_FETCH,
	});
	expect(response.ok()).toBeTruthy();
	const body = await response.json();
	const roles: Array<{ id: number; name: string }> = body.data ?? body;
	const role = roles.find((entry) => entry.name === name);
	expect(role).toBeDefined();
	if (!role) throw new Error(`workspace role ${name} not found`);
	return role.id;
}

test.describe('Workspace navigation configuration', () => {
	test('admin opens the config from the sidebar gear, toggles persist, and enforce', async ({
		page,
		request,
	}) => {
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`nav-config-${Date.now()}`)
		);

		await page.goto(`/workspaces/${workspace.id}`);
		await expect(page.getByTestId('workspace-nav-config-button')).toBeVisible();
		await page.getByTestId('workspace-nav-config-button').click();
		await expect(page).toHaveURL(new RegExp(`/workspaces/${workspace.id}/nav-config$`));
		await expect(page.getByTestId('navigation-config-page')).toBeVisible();

		// The queue is a views-group entry: it is configurable from the views
		// section, not from tools.
		await expect(
			page.getByTestId('nav-config-section-views').getByTestId('nav-config-row-queue')
		).toBeVisible();
		await expect(
			page.getByTestId('nav-config-section-tools').getByTestId('nav-config-row-queue')
		).toHaveCount(0);

		// Toggling a tools entry off dims its row and saves immediately.
		await expect(page.getByTestId('nav-config-row-agents')).toBeVisible();
		await expect(page.getByTestId('nav-config-row-agents')).not.toHaveClass(/opacity-40/);
		await page.getByTestId('nav-config-toggle-agents').click();
		await expect(page.getByTestId('nav-config-row-agents')).toHaveClass(/opacity-40/);
		// The sidebar reacts in place: no reload required.
		await expect(page.getByTestId('workspace-nav-agents')).toHaveCount(0);

		// Test entries are orientation only: no toggles, always visible.
		await expect(page.getByTestId('nav-config-row-test-cases')).toBeVisible();
		await expect(page.getByTestId('nav-config-toggle-test-cases')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-row-test-cases')).toContainText(
			"Always visible"
		);

		// The sidebar drops the entry, but the route still serves: tools toggles
		// are visibility-only and must not break deep links.
		await page.goto(`/workspaces/${workspace.id}`);
		await expect(page.getByTestId('workspace-nav-agents')).toHaveCount(0);
		await page.goto(`/workspaces/${workspace.id}/agents`);
		await expect(page).toHaveURL(new RegExp(`/workspaces/${workspace.id}/agents$`));
		await expect(page.getByTestId('agent-catalog')).toBeVisible();

		// The dimmed state survived the reload; re-enabling restores the entry.
		await page.goto(`/workspaces/${workspace.id}/nav-config`);
		await expect(page.getByTestId('nav-config-row-agents')).toHaveClass(/opacity-40/);
		await page.getByTestId('nav-config-toggle-agents').click();
		await expect(page.getByTestId('nav-config-row-agents')).not.toHaveClass(/opacity-40/);
		await expect(page.getByTestId('workspace-nav-agents')).toBeVisible();
		await page.goto(`/workspaces/${workspace.id}`);
		await expect(page.getByTestId('workspace-nav-agents')).toBeVisible();

		// The support queue is opt-in: off by default, enabled from the config
		// view, and its direct URL bounces while hidden.
		await expect(page.getByTestId('workspace-nav-queue')).toHaveCount(0);
		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page).not.toHaveURL(
			new RegExp(`/workspaces/${workspace.id}/queue$`),
		);
		await page.goto(`/workspaces/${workspace.id}/nav-config`);
		await expect(page.getByTestId('nav-config-row-queue')).toHaveClass(/opacity-40/);
		await page.getByTestId('nav-config-toggle-queue').click();
		await expect(page.getByTestId('nav-config-row-queue')).not.toHaveClass(/opacity-40/);
		await expect(page.getByTestId('workspace-nav-queue')).toBeVisible();
		await page.goto(`/workspaces/${workspace.id}`);
		await expect(page.getByTestId('workspace-nav-queue')).toBeVisible();
		await page.goto(`/workspaces/${workspace.id}/queue`);
		await expect(page).toHaveURL(new RegExp(`/workspaces/${workspace.id}/queue$`));
		await page.goto(`/workspaces/${workspace.id}/nav-config`);
		await page.getByTestId('nav-config-toggle-queue').click();
		await expect(page.getByTestId('nav-config-row-queue')).toHaveClass(/opacity-40/);
	});

	test('collection scope overrides views only and reset inherits again', async ({
		page,
		request,
	}) => {
		await authenticateAdminRequest(request);
		const stamp = Date.now();
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`nav-config-scope-${stamp}`)
		);
		const collection = await createCollectionViaAPI(request, {
			name: `Nav override ${stamp}`,
			workspace_id: workspace.id,
			ql_query: '',
		});
		// Exists before the first page load so the sidebar dropdown lists it.
		const otherCollection = await createCollectionViaAPI(request, {
			name: `Nav switch ${stamp}`,
			workspace_id: workspace.id,
			ql_query: '',
		});

		// Give the workspace a tools override first: the collection's effective
		// set then inherits workspace-only ids, which must never round-trip into
		// a collection-scope save.
		await page.goto(`/workspaces/${workspace.id}/nav-config`);
		await page.getByTestId('nav-config-toggle-milestones').click();
		await expect(page.getByTestId('nav-config-row-milestones')).toHaveClass(/opacity-40/);

		// The workspace default context has no inherited badge and no reset.
		await page.goto(`/workspaces/${workspace.id}/nav-config`);
		await expect(page.getByTestId('navigation-config-page')).toBeVisible();
		// The tools override survived the reload.
		await expect(page.getByTestId('nav-config-row-milestones')).toHaveClass(/opacity-40/);
		await expect(page.getByTestId('nav-config-inherited-badge')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-reset')).toHaveCount(0);

		// The collection sidebar section offers its own navigation entry and
		// the Tools-header gear stays a workspace-default-context affordance.
		await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/board`);
		await expect(page.getByTestId('workspace-nav-config-collection')).toBeVisible();
		await expect(page.getByTestId('workspace-nav-config-button')).toHaveCount(0);
		await page.getByTestId('workspace-nav-config-collection').click();
		await expect(page).toHaveURL(
			new RegExp(`/workspaces/${workspace.id}/collections/${collection.id}/nav-config$`)
		);
		await expect(page.getByTestId('nav-config-inherited-badge')).toBeVisible();

		// Workspace-only tools are not configurable at collection scope: the
		// tools section is absent even though milestones is overridden at the
		// workspace, the workspace-scoped queue view is absent too, and tests
		// stay an orientation-only section.
		await expect(page.getByTestId('nav-config-section-tools')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-row-queue')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-row-milestones')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-row-iterations')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-row-test-cases')).toBeVisible();
		await expect(page.getByTestId('nav-config-toggle-test-cases')).toHaveCount(0);

		// Switching collections from the config page keeps a working URL.
		await page.getByTestId('workspace-collection-select').click();
		await page
			.getByTestId(`workspace-collection-option-${otherCollection.id}`)
			.click();
		await expect(page).toHaveURL(
			new RegExp(`/workspaces/${workspace.id}/collections/${otherCollection.id}/nav-config$`)
		);
		await expect(page.getByTestId('navigation-config-page')).toBeVisible();
		await expect(page.getByTestId('nav-config-inherited-badge')).toBeVisible();

		await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/nav-config`);
		await expect(page.getByTestId('navigation-config-page')).toBeVisible();
		await expect(page.getByTestId('nav-config-inherited-badge')).toBeVisible();

		// Toggling a legit collection view saves cleanly: no error toast, and
		// the row dims because the PUT succeeded.
		await page.getByTestId('nav-config-toggle-tree').click();
		await expect(page.getByTestId('nav-config-row-tree')).toHaveClass(/opacity-40/);
		// The collection sidebar reacts in place: no reload required.
		await expect(page.getByTestId('workspace-nav-tree')).toHaveCount(0);
		await expect(
			page.locator('[data-testid="toast"][data-toast-variant="error"]')
		).toHaveCount(0);
		await expect(page.getByTestId('nav-config-inherited-badge')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-reset')).toBeVisible();

		// The override applies to the collection only; the workspace default
		// context keeps the view.
		await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/board`);
		await expect(page.getByTestId('workspace-nav-tree')).toHaveCount(0);
		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId('workspace-nav-tree')).toBeVisible();

		// Resetting inherits the workspace set again.
		await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/nav-config`);
		await page.getByTestId('nav-config-reset').click();
		await expect(page.getByTestId('nav-config-inherited-badge')).toBeVisible();
		await expect(page.getByTestId('workspace-nav-tree')).toBeVisible();
		await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/board`);
		await expect(page.getByTestId('workspace-nav-tree')).toBeVisible();
	});

	test('global collections configure views from the switcher, gated to the creator', async ({
		page,
		browser,
		request,
		baseURL,
	}) => {
		await authenticateAdminRequest(request);
		const suffix = `nav-config-global-${Date.now()}`;
		// No workspace_id: a global collection.
		const collection = await createCollectionViaAPI(request, {
			name: `Global nav ${suffix}`,
			ql_query: '',
		});

		// The creator gets a gear in the view switcher leading to the global
		// config surface, which exposes only the views section.
		await page.goto(`/collections/${collection.id}/board`);
		await expect(page.getByTestId('board-view-switcher')).toBeVisible();
		await expect(page.getByTestId('collection-nav-config-button')).toBeVisible();
		await page.getByTestId('collection-nav-config-button').click();
		await expect(page).toHaveURL(new RegExp(`/collections/${collection.id}/nav-config$`));
		await expect(page.getByTestId('navigation-config-page')).toBeVisible();
		await expect(page.getByTestId('nav-config-row-board')).toBeVisible();
		await expect(page.getByTestId('nav-config-row-overview')).toHaveCount(0);
		await expect(page.getByTestId('nav-config-section-tools')).toHaveCount(0);

		// Toggling a view off hides it from the switcher; reset restores it.
		await page.getByTestId('nav-config-toggle-backlog').click();
		await expect(page.getByTestId('nav-config-row-backlog')).toHaveClass(/opacity-40/);
		await page.goto(`/collections/${collection.id}/board`);
		await expect(page.getByTestId('board-view-switcher')).toBeVisible();
		await expect(page.getByTestId('switcher-view-backlog')).toHaveCount(0);

		await page.goto(`/collections/${collection.id}/nav-config`);
		await page.getByTestId('nav-config-reset').click();
		await expect(page.getByTestId('nav-config-inherited-badge')).toBeVisible();
		await page.goto(`/collections/${collection.id}/board`);
		await expect(page.getByTestId('switcher-view-backlog')).toBeVisible();

		// A user who cannot edit the collection gets neither the gear nor the
		// Configure button.
		const otherData = generateUser(suffix);
		await createUserViaAPI(request, otherData);
		let otherContext;
		try {
			otherContext = await browser.newContext({
				baseURL,
				storageState: { cookies: [], origins: [] },
			});
			const login = await otherContext.request.post('/api/auth/login', {
				headers: SEC_FETCH,
				data: {
					email_or_username: otherData.username,
					password: otherData.password_hash,
					remember_me: false,
				},
			});
			expect(login.ok()).toBeTruthy();
			const otherPage = await otherContext.newPage();
			await otherPage.goto(`/collections/${collection.id}/board`);
			await expect(otherPage.getByTestId('board-view-switcher')).toBeVisible();
			await expect(otherPage.getByTestId('collection-nav-config-button')).toHaveCount(0);
		} finally {
			await otherContext?.close();
		}
	});

	test('viewer members see neither the gear entry point nor the page affordances', async ({
		browser,
		request,
		baseURL,
	}) => {
		await authenticateAdminRequest(request);
		const suffix = `nav-config-viewer-${Date.now()}`;
		const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const collection = await createCollectionViaAPI(request, {
			name: `Viewer nav ${suffix}`,
			workspace_id: workspace.id,
			ql_query: '',
		});
		const userData = generateUser(suffix);
		const user = await createUserViaAPI(request, userData);
		const viewerRole = await workspaceRoleId(request, 'Viewer');
		const assignment = await request.post('/api/workspace-roles/assign', {
			headers: SEC_FETCH,
			data: {
				user_id: user.id,
				workspace_id: workspace.id,
				role_id: viewerRole,
			},
		});
		expect(assignment.ok()).toBeTruthy();

		let viewerContext: BrowserContext | undefined;
		try {
			viewerContext = await browser.newContext({
				baseURL,
				storageState: { cookies: [], origins: [] },
			});
			const login = await viewerContext.request.post('/api/auth/login', {
				headers: SEC_FETCH,
				data: {
					email_or_username: userData.username,
					password: userData.password_hash,
					remember_me: false,
				},
			});
			expect(login.ok()).toBeTruthy();

			const viewerPage = await viewerContext.newPage();
			await viewerPage.goto(`/workspaces/${workspace.id}`);
			await expect(viewerPage.getByTestId('workspace-nav-config-button')).toHaveCount(0);

			// The collection-section entry point is equally hidden.
			await viewerPage.goto(
				`/workspaces/${workspace.id}/collections/${collection.id}/board`
			);
			await expect(viewerPage.getByTestId('workspace-nav-config-collection')).toHaveCount(0);
		} finally {
			await viewerContext?.close();
		}
	});
});
