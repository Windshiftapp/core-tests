import type { APIRequestContext, Page } from '@playwright/test';
import { createItemViaAPI, createWorkspaceViaAPI, updateItemViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

type CatalogEntry = { id: number; name: string };

async function catalog(request: APIRequestContext, path: string): Promise<CatalogEntry[]> {
  const response = await request.get(path, {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  return body.data ?? body;
}

async function chooseMultiFilter(
  page: Page,
  scope: 'workspace' | 'status' | 'priority',
  id: number
) {
  const filter = page.getByTestId(`global-search-${scope}-filter`);
  await filter.locator('input[type="text"]').click();
  await page.getByTestId(`global-search-${scope}-option-${id}`).click();
  await page.keyboard.press('Escape');
}

test.describe('WI-696: critical global-search journeys', () => {
  test('searches by title and item key, renders metadata, and opens detail', async ({
    page,
    request,
  }) => {
    const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi696-title-key-${stamp}`)
    );
    const title = `WI696 searchable title ${stamp}`;
    const item = await createItemViaAPI(request, workspace.id, {
      title,
      description: `metadata journey ${stamp}`,
    });
    const itemKey = `${workspace.key}-${item.workspace_item_number}`;

    await page.goto('/search');
    await expect(page.getByTestId('global-search-page')).toBeVisible();
    await expect(page.getByTestId('global-search-prompt')).toBeVisible();

    const query = page.getByTestId('global-search-query');
    await query.fill(title);
    await query.press('Enter');

    const row = page.getByTestId(`global-search-result-${item.id}`);
    await expect(row).toBeVisible();
    await expect(row).toContainText(itemKey);
    await expect(row).toContainText(title);
    await expect(row).toContainText(workspace.name);
    await expect(row).toContainText(item.status_name);
    if (item.priority_name) await expect(row).toContainText(item.priority_name);

    await query.fill(itemKey);
    await query.press('Enter');
    await expect(row).toBeVisible();
    await expect(row).toContainText(itemKey);

    await row.click();
    await expect(page).toHaveURL(
      new RegExp(`/workspaces/${workspace.id}/items/${item.id}(?:$|[?#])`)
    );
  });

  test('combines filters and restores them across reload and browser history', async ({
    page,
    request,
  }) => {
    const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const queryToken = `WI696_FILTER_QUERY_${stamp}`;
    const exactTitle = `WI696 exact ${stamp}`;
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi696-filters-${stamp}`)
    );
    const otherWorkspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi696-filters-other-${stamp}`)
    );
    const statuses = await catalog(request, '/api/statuses');
    const priorities = await catalog(request, '/api/priorities');
    const openStatus = statuses.find((status) => status.name === 'Open') ?? statuses[0];
    const otherStatus =
      statuses.find((status) => status.name === 'In Progress') ??
      statuses.find((status) => status.id !== openStatus.id);
    const highPriority = priorities.find((priority) => priority.name === 'High') ?? priorities[0];
    const otherPriority =
      priorities.find((priority) => priority.name === 'Low') ??
      priorities.find((priority) => priority.id !== highPriority.id);
    expect(openStatus).toBeDefined();
    expect(otherStatus).toBeDefined();
    expect(highPriority).toBeDefined();
    expect(otherPriority).toBeDefined();

    const makeItem = (workspaceId: number, title: string) =>
      createItemViaAPI(request, workspaceId, {
        title,
        description: queryToken,
      });

    const target = await makeItem(workspace.id, exactTitle);
    const wrongWorkspace = await makeItem(otherWorkspace.id, exactTitle);
    const wrongStatus = await makeItem(workspace.id, exactTitle);
    const wrongPriority = await makeItem(workspace.id, exactTitle);
    const wrongDynamicValue = await makeItem(workspace.id, `${exactTitle} other`);

    await updateItemViaAPI(request, target.id, {
      priority_id: highPriority.id,
    });
    await updateItemViaAPI(request, wrongWorkspace.id, {
      priority_id: highPriority.id,
    });
    await updateItemViaAPI(request, wrongStatus.id, {
      priority_id: highPriority.id,
    });
    const transition = await request.post(`/api/items/${wrongStatus.id}/transition`, {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: { to_status_id: otherStatus.id },
    });
    expect(transition.ok()).toBeTruthy();
    await updateItemViaAPI(request, wrongPriority.id, {
      priority_id: otherPriority.id,
    });
    await updateItemViaAPI(request, wrongDynamicValue.id, {
      priority_id: highPriority.id,
    });

    await page.goto('/search');
    const query = page.getByTestId('global-search-query');
    await query.fill(queryToken);
    await query.press('Enter');
    await chooseMultiFilter(page, 'workspace', workspace.id);
    await chooseMultiFilter(page, 'status', openStatus.id);
    await chooseMultiFilter(page, 'priority', highPriority.id);

    await page.getByTestId('global-search-add-dynamic-filter').click();
    const dynamicFilter = page.getByTestId('global-search-dynamic-filter-0');
    await dynamicFilter.getByTestId('field-selector-trigger').click();
    await page.getByTestId('field-option-title').click();
    await dynamicFilter.getByTestId('global-search-dynamic-filter-0-value').click();
    await page.getByTestId('global-search-dynamic-filter-0-value-input').fill(exactTitle);
    await page.getByTestId('global-search-dynamic-filter-0-apply-value').click();

    const targetRow = page.getByTestId(`global-search-result-${target.id}`);
    await expect(targetRow).toBeVisible();
    await expect(page.getByTestId(`global-search-result-${wrongWorkspace.id}`)).toHaveCount(0);
    await expect(page.getByTestId(`global-search-result-${wrongStatus.id}`)).toHaveCount(0);
    await expect(page.getByTestId(`global-search-result-${wrongPriority.id}`)).toHaveCount(0);
    await expect(page.getByTestId(`global-search-result-${wrongDynamicValue.id}`)).toHaveCount(0);

    const filteredURL = new URL(page.url());
    expect(filteredURL.searchParams.get('search')).toBe(queryToken);
    expect(filteredURL.searchParams.get('workspaces')).toBe(String(workspace.id));
    expect(filteredURL.searchParams.get('statuses')).toBe(String(openStatus.id));
    expect(filteredURL.searchParams.get('priorities')).toBe(String(highPriority.id));
    expect(JSON.parse(filteredURL.searchParams.get('dynamicFilters') ?? '[]')).toMatchObject([
      { field: { id: 'title' }, operator: '=', value: exactTitle },
    ]);

    await page.reload();
    await expect(targetRow).toBeVisible();
    await expect(query).toHaveValue(queryToken);
    await expect(page.getByTestId('global-search-workspace-filter')).toContainText(workspace.name);
    await expect(page.getByTestId('global-search-status-filter')).toContainText(openStatus.name);
    await expect(page.getByTestId('global-search-priority-filter')).toContainText(
      highPriority.name
    );
    await expect(dynamicFilter.getByTestId('field-selector-value')).toHaveText('Title');
    await expect(dynamicFilter.getByTestId('global-search-dynamic-filter-0-value')).toContainText(
      exactTitle
    );

    const noMatch = `WI696_NO_MATCH_${stamp}`;
    await query.fill(noMatch);
    await query.press('Enter');
    await expect(page.getByTestId('global-search-empty-results')).toBeVisible();

    await page.goBack();
    await expect(query).toHaveValue(queryToken);
    await expect(targetRow).toBeVisible();

    await page.goForward();
    await expect(query).toHaveValue(noMatch);
    await expect(page.getByTestId('global-search-empty-results')).toBeVisible();

    await page.goBack();
    await expect(targetRow).toBeVisible();
  });

  test('keeps query and filters visible through failure and successful retry', async ({
    page,
    request,
  }) => {
    const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi696-retry-${stamp}`)
    );
    const title = `WI696 retry ${stamp}`;
    const item = await createItemViaAPI(request, workspace.id, { title });
    const referenceDataResponses = Promise.all(
      ['/api/workspaces', '/api/statuses', '/api/priorities', '/api/status-categories'].map(
        (path) =>
          page.waitForResponse((response) => {
            const url = new URL(response.url());
            return response.request().method() === 'GET' && url.pathname === path;
          })
      )
    );

    await page.goto('/search');
    for (const response of await referenceDataResponses) {
      expect(response.ok()).toBeTruthy();
    }
    await expect(page.getByTestId('global-search-prompt')).toBeVisible();

    let failNextSearch = true;

    await page.route('**/api/items?**', async (route) => {
      const url = new URL(route.request().url());
      const ql = url.searchParams.get('ql') ?? '';
      if (failNextSearch && ql.includes(`title ~ "${title}"`)) {
        failNextSearch = false;
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Search temporarily unavailable' }),
        });
        return;
      }
      await route.continue();
    });

    const query = page.getByTestId('global-search-query');
    await query.fill(title);
    const failedSearchResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      const ql = url.searchParams.get('ql') ?? '';
      return (
        response.request().method() === 'GET' &&
        url.pathname === '/api/items' &&
        ql.includes(`title ~ "${title}"`) &&
        response.status() === 503
      );
    });
    await query.press('Enter');
    await failedSearchResponse;

    await expect(page.getByTestId('global-search-error')).toBeVisible();
    await expect(page.getByTestId('global-search-error')).toContainText(
      'Search temporarily unavailable'
    );
    await expect(query).toHaveValue(title);
    expect(new URL(page.url()).searchParams.get('search')).toBe(title);

    await page.getByTestId('global-search-retry').click();
    await expect(page.getByTestId(`global-search-result-${item.id}`)).toBeVisible();
    await expect(query).toHaveValue(title);
    expect(new URL(page.url()).searchParams.get('search')).toBe(title);
  });
});
