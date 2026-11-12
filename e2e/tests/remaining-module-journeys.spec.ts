import type { Route } from '@playwright/test';
import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';
import { generateWorkspace } from '../fixtures/test-data';

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  });

test.describe('Remaining 0.8.5 module journeys (WI-706)', () => {
  test('collection UI creates a collection, searches its items, recovers from save failure, and persists edits', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/Cancelled/);
    allowConsoleError(/\/api\/v2\/collections\/\d+\/board-configuration/);
    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi706-collection-${stamp}`)
    );
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Searchable delivery ${stamp}`,
    });
    const originalName = `Browser collection ${stamp}`;
    const revisedName = `${originalName} revised`;

    await page.goto('/collections');
    await page.getByTestId('collection-create').click();
    await page.getByTestId('collection-create-name').fill(originalName);
    const createdResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith('/api/v2/collections')
    );
    await page.locator('#create-modal-submit').click();
    const collectionResponse = await createdResponse;
    expect(collectionResponse.ok()).toBeTruthy();
    const collection = (await collectionResponse.json()).data as { id: number };
    await expect(page).toHaveURL(new RegExp(`/collections/${collection.id}$`));

    await page.getByTestId('collection-search-open').click();
    await page.getByTestId('collection-search-input').fill(item.title);
    await page.getByTestId('collection-search-apply').click();
    await expect(page.getByTestId(`collection-result-${item.id}`)).toBeVisible({
      timeout: 15_000,
    });
    await page.getByTestId('collection-name').fill(revisedName);

    let saveAttempts = 0;
    await page.route(`**/api/v2/collections/${collection.id}`, async (route, req) => {
      if (req.method() === 'PATCH' && saveAttempts++ === 0) {
        await json(route, { error: 'Temporary collection failure' }, 503);
        return;
      }
      await route.continue();
    });
    allowConsoleError(new RegExp(`/api/v2/collections/${collection.id}`));
    allowConsoleError(/Failed to update collection/);

    await page.getByTestId('collection-save').click();
    await expect(page.getByTestId('toast').first()).toHaveAttribute('data-toast-variant', 'error');
    await expect(page.getByTestId('collection-name')).toHaveValue(revisedName);

    const savedResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'PATCH' &&
        new URL(response.url()).pathname.endsWith(`/api/v2/collections/${collection.id}`)
    );
    await page.getByTestId('collection-save').click();
    expect((await savedResponse).ok()).toBeTruthy();
    await expect(page).toHaveURL(/\/collections$/);
    await expect(page.getByTestId(`collection-row-${collection.id}`)).toBeVisible();

    await page.goto(`/collections/${collection.id}`);
    await expect(page.getByTestId('collection-name')).toHaveValue(revisedName);
    await expect(page.getByTestId(`collection-result-${item.id}`)).toBeVisible({
      timeout: 15_000,
    });
  });

  test('source control and issue sync expose visible failures and persist successful administration changes', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi706-admin-${Date.now()}`)
    );
    const provider = {
      id: 501,
      name: 'Mock GitHub',
      provider_type: 'github',
      is_connected: false,
    };
    const repository = {
      id: 701,
      repository_name: 'windshift/release-testing',
      repository_url: 'https://example.test/windshift/release-testing',
      default_branch: 'main',
      milestone_tag_pattern: 'v*',
      milestone_branch_pattern: 'release/*',
    };
    let connection: {
      id: number;
      scm_provider_id: number;
      provider_name: string;
      provider_type: string;
      repository_count: number;
      smart_commits_enabled: boolean;
    } | null = null;
    let scmUpdateAttempts = 0;
    let issueSaveAttempts = 0;
    let issueConfig: Record<string, unknown> | null = null;

    allowConsoleError(/Failed to update smart commits setting/);
    allowConsoleError(/\/api\/workspaces\/\d+\/scm-connections/);
    allowConsoleError(/\/api\/workspaces\/\d+\/issue-sync/);

    await page.route(`**/api/workspaces/${workspace.id}/**`, async (route, req) => {
      const url = new URL(req.url());
      const apiPath = url.pathname.slice(url.pathname.indexOf('/api/'));
      const base = `/api/workspaces/${workspace.id}`;
      const method = req.method();

      if (apiPath === `${base}/scm-providers` && method === 'GET') {
        await json(route, [{ ...provider, is_connected: connection !== null }]);
        return;
      }
      if (apiPath === `${base}/scm-connections` && method === 'GET') {
        const connections = connection
          ? [
              {
                ...connection,
                ...(url.searchParams.get('include_auth_status') === 'true'
                  ? {
                      auth_status: {
                        auth_method: 'pat',
                        has_workspace_token: true,
                      },
                    }
                  : {}),
                ...(url.searchParams.get('include_repositories') === 'true'
                  ? { repositories: [repository] }
                  : {}),
              },
            ]
          : [];
        await json(route, connections);
        return;
      }
      if (apiPath === `${base}/scm-connections` && method === 'POST') {
        connection = {
          id: 601,
          scm_provider_id: provider.id,
          provider_name: provider.name,
          provider_type: provider.provider_type,
          repository_count: 1,
          smart_commits_enabled: false,
        };
        await json(route, connection, 201);
        return;
      }
      if (apiPath === `${base}/scm-connections/601` && method === 'PUT') {
        scmUpdateAttempts += 1;
        if (scmUpdateAttempts === 1) {
          await json(route, { error: 'Temporary SCM failure' }, 503);
          return;
        }
        const payload = req.postDataJSON() as {
          smart_commits_enabled: boolean;
        };
        if (!connection) throw new Error('SCM connection missing during update');
        connection = { ...connection, ...payload };
        await json(route, connection);
        return;
      }
      if (apiPath === `${base}/scm-connections/601/repositories` && method === 'GET') {
        await json(route, [repository]);
        return;
      }

      if (apiPath === `${base}/issue-sync` && method === 'GET') {
        if (!issueConfig) {
          await json(route, { error: 'issue sync config not found' }, 404);
          return;
        }
        await json(route, issueConfig);
        return;
      }
      if (apiPath === `${base}/issue-sync` && method === 'POST') {
        issueSaveAttempts += 1;
        if (issueSaveAttempts === 1) {
          await json(route, { error: 'Temporary issue sync failure' }, 503);
          return;
        }
        issueConfig = {
          id: 801,
          workspace_id: workspace.id,
          ...(req.postDataJSON() as Record<string, unknown>),
        };
        await json(route, issueConfig, 201);
        return;
      }
      if (apiPath === `${base}/issue-sync/status` && method === 'GET') {
        await json(route, {
          last_sync_at: null,
          last_sync_error: null,
          synced_item_count: 0,
        });
        return;
      }
      if (apiPath === `${base}/issue-sync/items` && method === 'GET') {
        await json(route, []);
        return;
      }

      await route.continue();
    });

    await page.goto(`/workspaces/${workspace.id}/settings/source-control`);
    await expect(page.getByTestId('workspace-scm-settings')).toBeVisible();
    await page.getByTestId(`scm-connect-${provider.id}`).click();
    await expect(page.getByTestId('scm-connection-601')).toBeVisible();
    await page.getByTestId('scm-connection-601').click();
    const smartCommits = page.getByTestId('scm-smart-commits-601');
    await smartCommits.click();
    await expect(page.getByTestId('toast').first()).toHaveAttribute('data-toast-variant', 'error');
    await expect(smartCommits).not.toBeChecked();

    const scmSaved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith(
          `/api/workspaces/${workspace.id}/scm-connections/601`
        )
    );
    await smartCommits.check();
    expect((await scmSaved).ok()).toBeTruthy();
    await page.reload();
    await page.getByTestId('scm-connection-601').click();
    await expect(page.getByTestId('scm-smart-commits-601')).toBeChecked();

    await page.goto(`/workspaces/${workspace.id}/settings/issue-sync`);
    await expect(page.getByTestId('issue-sync-settings')).toBeVisible();
    await page.getByTestId('issue-sync-repository').selectOption(String(repository.id));
    await page.getByTestId('issue-sync-enabled').click();
    await page.getByTestId('issue-sync-save').click();
    await expect(page.getByTestId('toast').first()).toHaveAttribute('data-toast-variant', 'error');
    await expect(page.getByTestId('issue-sync-repository')).toHaveValue(String(repository.id));
    await expect(page.getByTestId('issue-sync-enabled')).toHaveAttribute('aria-checked', 'true');

    const issueSaved = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith(`/api/workspaces/${workspace.id}/issue-sync`)
    );
    await page.getByTestId('issue-sync-save').click();
    expect((await issueSaved).ok()).toBeTruthy();
    await page.reload();
    await expect(page.getByTestId('issue-sync-repository')).toHaveValue(String(repository.id));
    await expect(page.getByTestId('issue-sync-enabled')).toHaveAttribute('aria-checked', 'true');
  });
});
