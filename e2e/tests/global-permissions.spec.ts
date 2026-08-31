import {
  createCollectionViaAPI,
  createCustomerOrgViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import {
  type APIRequestContext,
  type Browser,
  type BrowserContext,
  expect,
  type Page,
  test,
} from '../fixtures/context-path';
import {
  generateItem,
  generateIteration,
  generateMilestone,
  generateTeam,
  generateTimeProject,
  generateUser,
  generateWorkspace,
} from '../fixtures/test-data';
import { IterationPage } from '../pages/iteration.page';
import { MilestonePage } from '../pages/milestone.page';
import { TeamsPage } from '../pages/teams.page';
import { TimeTrackingPage } from '../pages/time-tracking.page';
import { WorkspacePage } from '../pages/workspace.page';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

type PermissionKey =
  | 'workspace.create'
  | 'milestone.create'
  | 'iteration.manage'
  | 'user.list'
  | 'asset.manage'
  | 'customers.manage'
  | 'project.manage'
  | 'action.set_actor'
  | 'teams.manage'
  | 'public_board.manage';

interface GrantedUser {
  id: number;
  username: string;
  password: string;
  request: APIRequestContext;
  context: BrowserContext;
  page: Page;
}

async function permissionIdByKey(request: APIRequestContext, key: PermissionKey): Promise<number> {
  const resp = await request.get('/api/permissions', { headers: SEC_FETCH });
  await expectStatus(resp, 200, 'GET /api/permissions');
  const permissions = (await resp.json()) as Array<{
    id: number;
    permission_key: string;
    scope: string;
  }>;
  const permission = permissions.find((p) => p.permission_key === key && p.scope === 'global');
  if (!permission) throw new Error(`global permission ${key} should be seeded`);
  return permission.id;
}

async function expectStatus(
  response: { status: () => number; text: () => Promise<string> },
  status: number,
  label: string
): Promise<void> {
  if (response.status() !== status) {
    throw new Error(
      `${label}: expected ${status}, got ${response.status()} ${await response.text()}`
    );
  }
}

async function createSinglePermissionUser(
  admin: APIRequestContext,
  playwright: any,
  key: PermissionKey
): Promise<Omit<GrantedUser, 'context' | 'page'>> {
  const userData = generateUser(`gp-${key.replace(/[^a-z0-9]/gi, '-')}`);
  const user = await admin.post('/api/users', {
    headers: SEC_FETCH,
    data: {
      email: userData.email,
      username: userData.username,
      first_name: userData.first_name,
      last_name: userData.last_name,
      password: userData.password_hash,
    },
  });
  await expectStatus(user, 201, 'create user');
  const created = await user.json();

  const activate = await admin.post(`/api/users/${created.id}/activate`, { headers: SEC_FETCH });
  expect(activate.ok(), `activate user: ${activate.status()}`).toBeTruthy();

  const permissionId = await permissionIdByKey(admin, key);
  const grant = await admin.post('/api/permissions/global/grant', {
    headers: SEC_FETCH,
    data: { user_id: created.id, permission_id: permissionId },
  });
  expect(grant.ok(), `grant ${key}: ${grant.status()}`).toBeTruthy();

  const request = await playwright.request.newContext({ baseURL: BASE_URL });
  const login = await request.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: {
      email_or_username: userData.username,
      password: userData.password_hash,
      remember_me: false,
    },
  });
  expect(login.ok(), `login ${key}: ${login.status()}`).toBeTruthy();

  return {
    id: created.id,
    username: userData.username,
    password: userData.password_hash,
    request,
  };
}

async function createBrowserForUser(
  browser: Browser,
  user: Omit<GrantedUser, 'context' | 'page'>
): Promise<GrantedUser> {
  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const login = await context.request.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: {
      email_or_username: user.username,
      password: user.password,
      remember_me: false,
    },
  });
  expect(login.ok(), `browser login: ${login.status()}`).toBeTruthy();
  return {
    ...user,
    context,
    page: await context.newPage(),
  };
}

async function newGrantedUser(
  admin: APIRequestContext,
  playwright: any,
  browser: Browser,
  key: PermissionKey
): Promise<GrantedUser> {
  const user = await createSinglePermissionUser(admin, playwright, key);
  return createBrowserForUser(browser, user);
}

async function findRowByName(page: Page, name: string) {
  return page.locator('tbody tr').filter({ hasText: name }).first();
}

async function expectAdminGuard(page: Page, path: string) {
  await page.goto(path);
  await expect(page.locator('text=Required Permission')).toBeVisible({ timeout: 10000 });
  await expect(page.locator('text=system.admin')).toBeVisible();
}

async function createCustomerOrganisationViaUI(
  page: Page,
  data: { name: string; email?: string; description?: string }
) {
  await page.goto('/time/organizations');
  await page.waitForLoadState('networkidle');
  await page
    .locator('button')
    .filter({ hasText: /add organization/i })
    .click();

  const dialog = page.locator('div[role="dialog"]');
  await dialog.waitFor({ state: 'visible', timeout: 5000 });
  await page.locator('#customer-name-input').fill(data.name);
  if (data.description) {
    await dialog.locator('textarea').fill(data.description);
  }
  if (data.email) {
    await dialog.locator('input[type="email"]').fill(data.email);
  }
  await dialog
    .locator('button')
    .filter({ hasText: /create organization/i })
    .click();
  await dialog.waitFor({ state: 'detached', timeout: 10000 });
  await expect(await findRowByName(page, data.name)).toBeVisible({ timeout: 10000 });
}

async function editCustomerOrganisationViaUI(
  page: Page,
  currentName: string,
  data: { name: string; description?: string }
) {
  await page.goto('/time/organizations');
  await page.waitForLoadState('networkidle');
  const row = await findRowByName(page, currentName);
  await row.locator('button').last().click();
  await page.locator('button[role="menuitem"]').filter({ hasText: /edit/i }).click();

  const dialog = page.locator('div[role="dialog"]');
  await dialog.waitFor({ state: 'visible', timeout: 5000 });
  await page.locator('#customer-name-input').fill(data.name);
  if (data.description !== undefined) {
    await dialog.locator('textarea').fill(data.description);
  }
  await dialog
    .locator('button')
    .filter({ hasText: /update organization/i })
    .click();
  await dialog.waitFor({ state: 'detached', timeout: 10000 });
  await expect(await findRowByName(page, data.name)).toBeVisible({ timeout: 10000 });
}

async function deleteCustomerOrganisationViaUI(page: Page, name: string) {
  await page.goto('/time/organizations');
  await page.waitForLoadState('networkidle');
  const row = await findRowByName(page, name);
  await row.locator('button').last().click();
  await page
    .locator('button[role="menuitem"]')
    .filter({ hasText: /delete/i })
    .click();
  const dialog = page.locator('div[role="dialog"]');
  await dialog.waitFor({ state: 'visible', timeout: 5000 });
  await dialog.locator('[data-testid="dialog-confirm"]').click();
  await expect(await findRowByName(page, name)).not.toBeVisible({ timeout: 5000 });
}

async function openAssetSetActions(page: Page, setName: string) {
  const heading = page.locator('h2').filter({ hasText: setName }).first();
  await expect(heading).toBeVisible({ timeout: 10000 });
  await heading
    .locator('xpath=ancestor::div[contains(@class, "flex")][1]')
    .locator('button')
    .last()
    .click();
  await page
    .locator('button[role="menuitem"]')
    .first()
    .waitFor({ state: 'visible', timeout: 5000 });
}

async function createAssetSetViaUI(page: Page, data: { name: string; description?: string }) {
  await page.goto('/assets/settings');
  await page.waitForLoadState('networkidle');
  await page
    .locator('button')
    .filter({ hasText: /new set|create asset set/i })
    .first()
    .click();
  const dialog = page.locator('div[role="dialog"]');
  await dialog.waitFor({ state: 'visible', timeout: 5000 });
  await dialog.locator('input[type="text"]').fill(data.name);
  if (data.description) {
    await dialog.locator('textarea').fill(data.description);
  }
  await dialog.locator('[data-testid="dialog-confirm"]').click();
  await dialog.waitFor({ state: 'detached', timeout: 10000 });
  await expect(page.locator('h2').filter({ hasText: data.name }).first()).toBeVisible({
    timeout: 10000,
  });
}

async function editAssetSetViaUI(
  page: Page,
  currentName: string,
  data: { name: string; description?: string }
) {
  await page.goto('/assets/settings');
  await page.waitForLoadState('networkidle');
  await openAssetSetActions(page, currentName);
  await page.locator('button[role="menuitem"]').filter({ hasText: /edit/i }).click();
  const dialog = page.locator('div[role="dialog"]');
  await dialog.waitFor({ state: 'visible', timeout: 5000 });
  await dialog.locator('input[type="text"]').fill(data.name);
  if (data.description !== undefined) {
    await dialog.locator('textarea').fill(data.description);
  }
  await dialog.locator('[data-testid="dialog-confirm"]').click();
  await dialog.waitFor({ state: 'detached', timeout: 10000 });
  await expect(page.locator('h2').filter({ hasText: data.name }).first()).toBeVisible({
    timeout: 10000,
  });
}

test.describe('Global permissions for non-admin users', () => {
  test('workspace.create: can create workspaces, but cannot grant permissions', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'workspace.create');
    try {
      const ws = generateWorkspace('gp-workspace-create');
      const workspacePage = new WorkspacePage(user.page);

      await workspacePage.goto();
      await expect(user.page.locator('button').filter({ hasText: 'Add Workspace' })).toBeVisible();
      await workspacePage.clickCreate();
      await workspacePage.fillForm(ws);
      await workspacePage.clickSave();
      await user.page.locator('div[role="dialog"]').waitFor({ state: 'detached', timeout: 15000 });
      // A fresh workspace redirects from its transient detail URL to the
      // configured default view (board). Waiting for the final URL avoids a
      // race where the assertion occasionally observed the intermediate URL.
      await expect(user.page).toHaveURL(/\/workspaces\/\d+\/board$/);

      await user.page.goto('/admin/permissions');
      await expect(user.page.locator('text=Required Permission')).toBeVisible({ timeout: 10000 });
      await expect(user.page.locator('text=system.admin')).toBeVisible();
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('milestone.create: can manage global milestones, but not global iterations', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'milestone.create');
    try {
      const milestonePage = new MilestonePage(user.page);
      const milestone = generateMilestone('gp-milestone-create');
      await milestonePage.gotoGlobal();
      await milestonePage.createMilestone({
        name: milestone.name,
        target_date: milestone.target_date,
        description: 'single global permission',
        status: 'planning',
      });
      await milestonePage.changeStatusViaEdit(milestone.name, 'in-progress');
      await milestonePage.verifyStatus(milestone.name, 'In Progress');

      const iterationPage = new IterationPage(user.page);
      await iterationPage.gotoGlobal();
      await expect(
        user.page.locator('[data-testid="iteration-create-button"]').first()
      ).toBeHidden();

      await milestonePage.gotoGlobal();
      await milestonePage.deleteMilestone(milestone.name);
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('iteration.manage: can manage global iterations, but not global milestones', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'iteration.manage');
    try {
      const iterationPage = new IterationPage(user.page);
      const iteration = generateIteration('gp-iteration-manage');
      await iterationPage.gotoGlobal();
      await iterationPage.createIteration({
        name: iteration.name,
        start_date: iteration.start_date,
        end_date: iteration.end_date,
        status: 'planned',
      });
      await iterationPage.changeStatusViaEdit(iteration.name, 'active');
      await iterationPage.verifyStatus(iteration.name, 'Active');

      const milestonePage = new MilestonePage(user.page);
      await milestonePage.gotoGlobal();
      await expect(
        user.page.locator('[data-testid="milestone-create-button"]').first()
      ).toBeHidden();

      await iterationPage.gotoGlobal();
      await iterationPage.deleteIteration(iteration.name);
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('user.list: cannot use admin user management UI', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'user.list');
    try {
      await expectAdminGuard(user.page, '/admin/users');
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('asset.manage: can create asset sets, but cannot access system admin', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'asset.manage');
    try {
      const setName = `E2E GP Asset Set ${Date.now()}`;
      await createAssetSetViaUI(user.page, {
        name: setName,
        description: 'single permission',
      });

      const renamed = `${setName} Updated`;
      await editAssetSetViaUI(user.page, setName, {
        name: renamed,
        description: 'updated by single global permission',
      });

      await expectAdminGuard(user.page, '/admin');
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('customers.manage: can manage customer organisations, but not time projects', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'customers.manage');
    try {
      const name = `E2E GP Customer ${Date.now()}`;
      const renamed = `${name} Updated`;
      await createCustomerOrganisationViaUI(user.page, {
        name,
        email: `gp-customer-${Date.now()}@example.test`,
        description: 'single global permission',
      });
      await editCustomerOrganisationViaUI(user.page, name, {
        name: renamed,
        description: 'updated by single global permission',
      });

      await user.page.goto('/time/projects');
      await user.page.waitForLoadState('networkidle');
      await expect(user.page.locator('button').filter({ hasText: /add project/i })).toBeHidden();

      await deleteCustomerOrganisationViaUI(user.page, renamed);
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('project.manage: can manage time projects', async ({ request, playwright, browser }) => {
    const user = await newGrantedUser(request, playwright, browser, 'project.manage');
    try {
      const customer = await createCustomerOrgViaAPI(request, {
        name: `E2E GP Project Customer ${Date.now()}`,
        active: true,
      });

      const timePage = new TimeTrackingPage(user.page);
      const project = generateTimeProject('gp-project-manage');
      await timePage.createProject({
        name: project.name,
        description: project.description,
        customer: customer.name,
      });
      await timePage.verifyProjectExists(project.name);

      const renamed = `${project.name} Updated`;
      await timePage.editProject(project.name, {
        name: renamed,
        description: 'updated by single global permission',
      });
      await timePage.verifyProjectExists(renamed);
      await timePage.deleteProject(renamed);
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('teams.manage: can create teams, but not workspaces', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'teams.manage');
    try {
      const teamsPage = new TeamsPage(user.page);
      const team = generateTeam('gp-teams-manage');
      await teamsPage.createTeam({ name: team.name, description: team.description });
      await teamsPage.verifyTeamExists(team.name);

      const renamed = `${team.name} Updated`;
      await teamsPage.editTeam(team.name, {
        name: renamed,
        description: 'updated by single global permission',
      });
      await teamsPage.verifyTeamExists(renamed);

      await user.page.goto('/workspaces');
      await user.page.waitForLoadState('networkidle');
      await expect(user.page.locator('button').filter({ hasText: 'Add Workspace' })).toBeHidden();

      await teamsPage.deleteTeam(renamed);
      await teamsPage.verifyTeamDoesNotExist(renamed);
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test("public_board.manage: can publish own collection, but not someone else's collection", async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'public_board.manage');
    try {
      const workspace = await createWorkspaceViaAPI(
        request,
        generateWorkspace(`gp-public-board-${Date.now()}`)
      );
      const rolesResponse = await request.get('/api/workspace-roles', { headers: SEC_FETCH });
      await expectStatus(rolesResponse, 200, 'list workspace roles');
      const roles = (await rolesResponse.json()) as Array<{ id: number; name: string }>;
      const adminRole = roles.find((role) => role.name === 'Administrator');
      expect(adminRole).toBeDefined();
      if (!adminRole) throw new Error('Administrator workspace role is missing');
      const assignment = await request.post('/api/workspace-roles/assign', {
        headers: SEC_FETCH,
        data: { user_id: user.id, workspace_id: workspace.id, role_id: adminRole.id },
      });
      await expectStatus(assignment, 201, 'assign workspace administrator');

      // Setup: create a collection owned by the granted user so the browser can
      // exercise the public sharing UI.
      const own = await user.request.post('/api/collections', {
        headers: SEC_FETCH,
        data: {
          name: `E2E GP Public Board ${Date.now()}`,
          ql_query: `workspace != "${workspace.key}"`,
          is_public: false,
        },
      });
      await expectStatus(own, 201, 'create collection with unsafe persisted scope');
      const ownCollection = await own.json();
      const slug = `gp-public-${Date.now()}`;

      await user.page.goto(`/collections/${ownCollection.id}`);
      await user.page.waitForLoadState('networkidle');
      const rawEditor = user.page.locator('#ql-editor');
      const enterRawMode = user.page.getByTestId('ql-enter-raw-mode');
      await expect(rawEditor.or(enterRawMode)).toBeVisible({ timeout: 15000 });
      if (!(await rawEditor.isVisible())) {
        await enterRawMode.click();
        await user.page.getByTestId('dialog-confirm').click();
      }
      await expect(rawEditor).toBeVisible();
      const liveQuery = `workspace IN (${workspace.key})`;
      await rawEditor.fill(liveQuery);
      await user.page.getByTestId('public-board-button').click();
      await expect(user.page.getByTestId('public-board-dialog')).toBeVisible({ timeout: 10000 });
      await user.page.getByTestId('public-board-enabled').click();
      await user.page.getByTestId('public-board-slug').fill(slug);
      const saved = user.page.waitForResponse(
        (response) =>
          response.request().method() === 'PUT' &&
          new URL(response.url()).pathname.endsWith(`/api/collections/${ownCollection.id}`)
      );
      await user.page.getByTestId('public-board-save').click();
      const savedResponse = await saved;
      expect(savedResponse.status()).toBe(200);
      expect(savedResponse.request().postDataJSON()).toMatchObject({
        ql_query: liveQuery,
        is_public: true,
        public_slug: slug,
      });
      await expect(user.page.getByTestId('public-board-dialog')).toBeHidden();

      await user.page.reload();
      await expect(user.page.getByTestId('ql-query-summary')).toContainText(workspace.key);
      await user.page.getByTestId('public-board-button').click();
      await expect(user.page.locator('#public-board-enabled-input')).toBeChecked();
      await expect(user.page.getByTestId('public-board-preview')).toBeVisible();

      // Setup: someone else's collection. The granted user should not see the
      // public sharing controls for it.
      const adminCollection = await createCollectionViaAPI(request, {
        name: `E2E GP Admin Collection ${Date.now()}`,
        ql_query: '',
        is_public: false,
      });

      await user.page.goto(`/collections/${adminCollection.id}`);
      await user.page.waitForLoadState('networkidle');
      await expect(user.page.getByTestId('public-board-button')).toBeHidden();
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });

  test('action.set_actor: can set action actor only when paired with workspace action management', async ({
    request,
    playwright,
    browser,
  }) => {
    const user = await newGrantedUser(request, playwright, browser, 'action.set_actor');
    try {
      const ws = await createWorkspaceViaAPI(request, generateWorkspace('gp-action-actor'));
      await createItemViaAPI(request, ws.id, generateItem(ws.id, 'gp-action-actor'));

      const body = {
        name: `E2E GP Actor Action ${Date.now()}`,
        description: 'single global permission',
        trigger_type: 'manual',
        trigger_config: JSON.stringify({ respond_to_cascades: true }),
        nodes: [{ id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 }],
        edges: [],
        actor_user_id: user.id,
      };

      await user.page.goto(`/workspaces/${ws.id}/actions`);
      await expect(user.page.locator('text=Required Permission')).toBeVisible({ timeout: 10000 });

      const rolesResp = await request.get('/api/workspace-roles', { headers: SEC_FETCH });
      const roles = (await rolesResp.json()) as Array<{ id: number; name: string }>;
      const adminRole = roles.find((r) => r.name === 'Administrator');
      if (!adminRole) throw new Error('Administrator workspace role not found');
      const assign = await request.post('/api/workspace-roles/assign', {
        headers: SEC_FETCH,
        data: { user_id: user.id, workspace_id: ws.id, role_id: adminRole.id },
      });
      expect(assign.ok(), `assign Administrator: ${assign.status()}`).toBeTruthy();

      const created = await request.post(`/api/workspaces/${ws.id}/actions`, {
        headers: SEC_FETCH,
        data: body,
      });
      await expectStatus(created, 201, 'create action with actor');
      const action = await created.json();

      await user.page.goto(`/workspaces/${ws.id}/actions/${action.id}`);
      await expect(user.page.locator('[data-testid="user-picker-trigger"]').first()).toBeVisible({
        timeout: 15_000,
      });
    } finally {
      await user.context.close();
      await user.request.dispose();
    }
  });
});
