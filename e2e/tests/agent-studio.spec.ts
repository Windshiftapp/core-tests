import type { APIRequestContext, BrowserContext } from '@playwright/test';
import { createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
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

test.describe('Agent Studio responsive and authorization journey', () => {
  test('workspace admin can open the responsive catalog and create journey', async ({
    page,
    request,
  }) => {
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`agent-studio-admin-${Date.now()}`)
    );
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/workspaces/${workspace.id}/agents`);

    await expect(page.getByTestId('agent-catalog')).toBeVisible();
    await expect(page.getByTestId('agent-catalog-manage')).toBeVisible();
    await expect(page.getByTestId('agent-catalog-manage')).toContainText('A');
    await page.getByTestId('mobile-workspace-nav-trigger').click();
    await expect(
      page.getByTestId('workspace-tools-navigation').getByTestId('workspace-nav-agents')
    ).toBeVisible();
    await page.getByTestId('mobile-workspace-nav-trigger').click();
    await expect(page.getByTestId('mobile-workspace-nav-backdrop')).toBeHidden();
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      .toBe(true);

    await page.keyboard.press('a');
    await expect(page.getByTestId('agent-create')).toBeVisible();
    await expect(page.getByTestId('agent-template').first()).toBeVisible();
    await expect(page.getByTestId('agent-create-submit')).toContainText(/(?:⌘|Ctrl) ↵/);
  });

  test('runner onboarding keeps plaintext ephemeral and revokes when leaving creation', async ({
    page,
    request,
  }) => {
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`agent-studio-runner-${Date.now()}`)
    );
    const apiRoot = `**/api/workspaces/${workspace.id}`;
    await page.route(`${apiRoot}/agent-templates`, (route) =>
      route.fulfill({
        json: [
          {
            key: 'software_engineer',
            name: 'Software engineer',
            default_type: 'coding',
            instructions: 'Work in the configured repository.',
          },
        ],
      })
    );
    await page.route('**/api/llm/connections', (route) =>
      route.fulfill({
        json: [{ id: 9, name: 'Primary model', model: 'test-model' }],
      })
    );
    await page.route(`${apiRoot}/agent-binding-candidates`, (route) => route.fulfill({ json: [] }));
    await page.route(`${apiRoot}/agent-tool-capabilities`, (route) => route.fulfill({ json: [] }));
    await page.route(`${apiRoot}/scm-connections`, (route) => route.fulfill({ json: [] }));
    await page.route(`${apiRoot}/action-capabilities?type=runner_pool`, (route) =>
      route.fulfill({
        json: [
          {
            id: 7,
            name: 'Engineering runners',
            capability_type: 'runner_pool',
            is_enabled: true,
          },
        ],
      })
    );

    let revokeCalls = 0;
    await page.route(`${apiRoot}/agent-runner-pools/7/tokens`, async (route) => {
      if (route.request().method() === 'POST') {
        await route.fulfill({
          status: 201,
          json: {
            id: 31,
            token: 'wsrt_plaintext-once',
            install_command: 'docker run --rm windshift-runner',
            expires_at: new Date(Date.now() + 60_000).toISOString(),
          },
        });
        return;
      }
      await route.fulfill({
        json: [
          {
            id: 31,
            token_prefix: 'wsrt_demo',
            expires_at: new Date(Date.now() + 60_000).toISOString(),
          },
        ],
      });
    });
    await page.route(`${apiRoot}/agent-runner-pools/7/tokens/31`, async (route) => {
      revokeCalls += 1;
      await route.fulfill({ json: { id: 31, revoked: true } });
    });
    await page.route(`${apiRoot}/agent-runner-pools/7/instances`, (route) =>
      route.fulfill({ json: [] })
    );

    await page.goto(`/workspaces/${workspace.id}/agents/new`);
    await expect(page.getByTestId('agent-runner-setup')).toBeVisible();
    await page.locator('#agent-runner-mode').click();
    await page.locator('#agent-runner-mode-option-new').click();
    await page.locator('#agent-runner-pool').click();
    await page.locator('#agent-runner-pool-option-7').click();
    await page.getByTestId('agent-runner-generate').click();
    await expect(page.getByTestId('agent-runner-command')).toBeVisible();
    expect(
      await page.evaluate(() =>
        Object.values(window.localStorage).some((value) => value.includes('wsrt_plaintext-once'))
      )
    ).toBe(false);

    await page.getByTestId('agent-create-back').click();
    await expect.poll(() => revokeCalls).toBe(1);
    await page.goto(`/workspaces/${workspace.id}/agents/new`);
    await expect(page.getByTestId('agent-runner-cancel')).toHaveCount(0);
    await expect(page.getByTestId('agent-runner-command')).toHaveCount(0);
  });

  test('workspace viewer cannot see or bypass Agent Studio administration', async ({
    browser,
    request,
    baseURL,
  }) => {
    const suffix = `agent-studio-viewer-${Date.now()}`;
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
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
      await viewerPage.goto(`/workspaces/${workspace.id}/agents`);
      await expect(viewerPage.getByTestId('agent-catalog')).toBeVisible();
      await expect(viewerPage.getByTestId('workspace-nav-agents')).toHaveCount(0);
      await expect(viewerPage.getByTestId('agent-catalog-manage')).toHaveCount(0);
      await expect(viewerPage.getByTestId('agent-catalog-empty-manage')).toHaveCount(0);

      const templatesResponse = viewerPage.waitForResponse(
        (response) =>
          response.url().includes(`/api/workspaces/${workspace.id}/agent-templates`) &&
          response.request().method() === 'GET'
      );
      await viewerPage.goto(`/workspaces/${workspace.id}/agents/new`);
      expect((await templatesResponse).status()).toBe(403);
      await expect(viewerPage.getByTestId('agent-create')).toBeVisible();
      await expect(viewerPage.getByTestId('agent-template')).toHaveCount(0);
      await expect(viewerPage.getByTestId('agent-create-submit')).toHaveCount(0);
    } finally {
      await viewerContext?.close();
    }
  });
});
