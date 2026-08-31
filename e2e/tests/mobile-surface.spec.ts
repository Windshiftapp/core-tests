import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function configureOptionalCreateFields(
  request: APIRequestContext,
  workspaceId: number,
  suffix: string
) {
  const itemTypesResponse = await request.get('/api/item-types', {
    headers: SEC_FETCH,
  });
  expect(itemTypesResponse.ok()).toBeTruthy();
  const itemTypes = await itemTypesResponse.json();
  const itemTypeId = itemTypes[0]?.id;
  expect(itemTypeId).toBeGreaterThan(0);

  const screenResponse = await request.post('/api/screens', {
    headers: SEC_FETCH,
    data: { name: `Mobile optional fields ${suffix}` },
  });
  expect(screenResponse.ok()).toBeTruthy();
  const screen = await screenResponse.json();

  const fieldsResponse = await request.put(`/api/screens/${screen.id}/fields`, {
    headers: SEC_FETCH,
    data: ['priority', 'assignee', 'due_date', 'start_date', 'end_date'].map(
      (field_identifier, display_order) => ({
        field_type: 'system',
        field_identifier,
        display_order,
        is_required: false,
        field_width: 'full',
      })
    ),
  });
  expect(fieldsResponse.ok()).toBeTruthy();

  const configurationResponse = await request.post('/api/configuration-sets', {
    headers: SEC_FETCH,
    data: {
      name: `Mobile optional config ${suffix}`,
      workspace_ids: [workspaceId],
      create_screen_id: screen.id,
      edit_screen_id: screen.id,
      view_screen_id: screen.id,
      item_type_configs: [
        {
          item_type_id: itemTypeId,
          create_screen_id: screen.id,
          edit_screen_id: screen.id,
          view_screen_id: screen.id,
        },
      ],
    },
  });
  expect(configurationResponse.ok()).toBeTruthy();
  const configuration = await configurationResponse.json();

  return { configurationId: configuration.id, screenId: screen.id };
}

/**
 * Mobile PWA surface (/m/*). Exercises the phone shell at an iPhone-ish
 * viewport: bottom-nav tab switching across the four data-backed views, the
 * installable manifest, and the item-detail route. The shell is route-driven
 * (renders for any /m/* path regardless of viewport), so the small viewport is
 * realism rather than a gate.
 */

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

test.describe('Mobile surface', () => {
  test('bottom nav switches between the four views', {
    tag: '@critical-browser',
  }, async ({ page }) => {
    await page.goto('/m');
    await page.waitForLoadState('networkidle');

    await expect(page.getByTestId('mobile-shell')).toBeVisible();
    await expect(page.getByTestId('mobile-nav')).toBeVisible();

    // My Work is the default tab — header + the three segments render.
    await expect(page.getByTestId('mobile-header-title')).toHaveText('My Work');
    await expect(page.getByTestId('my-work-segment-assigned')).toBeVisible();
    await expect(page.getByTestId('my-work-segment-watched')).toBeVisible();
    await expect(page.getByTestId('my-work-segment-recent')).toBeVisible();

    await page.getByTestId('mobile-nav-personal').click();
    await expect(page.getByTestId('mobile-header-title')).toHaveText('Personal');

    await page.getByTestId('mobile-nav-timer').click();
    await expect(page.getByTestId('mobile-header-title')).toHaveText('Timer');
    await expect(page.getByTestId('timer-card')).toBeVisible();

    await page.getByTestId('mobile-nav-notifications').click();
    await expect(page.getByTestId('mobile-header-title')).toHaveText('Notifications');
    await expect(page.getByTestId('notifications-list')).toBeVisible();

    await page.getByTestId('mobile-nav-my-work').click();
    await expect(page.getByTestId('mobile-header-title')).toHaveText('My Work');
  });

  test('My Work segments each render a list or empty state', async ({ page }) => {
    await page.goto('/m');
    await page.waitForLoadState('networkidle');

    for (const seg of ['assigned', 'watched', 'recent']) {
      await page.getByTestId(`my-work-segment-${seg}`).click();
      // Either rows or the segment's empty message — both live under the list.
      await expect(page.getByTestId('my-work-list')).toBeVisible();
      const rows = page.getByTestId('mobile-item-row');
      const empty = page.getByTestId('my-work-empty');
      await expect
        .poll(async () => (await rows.count()) > 0 || (await empty.isVisible().catch(() => false)))
        .toBeTruthy();
    }
  });

  test('tapping a work item opens the mobile detail', async ({ page, request }) => {
    const meResponse = await request.get('/api/auth/me', {
      headers: SEC_FETCH,
    });
    expect(meResponse.ok()).toBeTruthy();
    const me = await meResponse.json();
    const currentUserId = me.user?.id ?? me.id;
    expect(currentUserId).toBeGreaterThan(0);

    const workspaceData = generateWorkspace('mobile-detail');
    const workspace = await createWorkspaceViaAPI(request, workspaceData);
    const itemData = generateItem(workspace.id, 'mobile-detail');
    const item = await createItemViaAPI(request, workspace.id, {
      title: itemData.title,
      description: itemData.description,
      assignee_id: currentUserId,
    });

    await page.goto('/m');
    await page.waitForLoadState('networkidle');

    const row = page.locator(`#mobile-item-row-${item.id}`);
    await expect(row).toBeVisible();
    await row.click();
    await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}$`));
    await expect(page.getByTestId('mobile-item-detail')).toBeVisible();
    await expect(page.getByTestId('detail-title')).toBeVisible();

    // Back returns to the shell.
    await page.getByTestId('mobile-header-back').click();
    await expect(page.getByTestId('mobile-nav')).toBeVisible();
  });

  test('serves an installable web manifest', async ({ page }) => {
    const res = await page.goto('/manifest.webmanifest');
    if (!res) throw new Error('manifest navigation did not return a response');
    expect(res.ok()).toBeTruthy();
    expect(res.headers()['content-type']).toContain('application/manifest+json');
    const manifest = await res.json();
    expect(manifest.start_url).toContain('m');
    expect(manifest.display).toBe('standalone');
    expect(manifest.display_override ?? []).not.toContain('window-controls-overlay');
  });

  test('search opens from My Work and queries items', async ({ page }) => {
    await page.goto('/m');
    await page.waitForLoadState('networkidle');

    await page.getByTestId('mobile-search-open').click();
    await expect(page).toHaveURL(/\/m\/search$/);
    const input = page.getByTestId('mobile-search-input');
    await expect(input).toBeVisible();
    // Before typing, the prompt shows; after typing, results or empty resolve.
    await expect(page.getByTestId('search-prompt')).toBeVisible();
    await input.fill('zzz-no-such-item-xyz');
    await expect
      .poll(
        async () =>
          (await page.getByTestId('mobile-item-row').count()) > 0 ||
          (await page
            .getByTestId('search-empty')
            .isVisible()
            .catch(() => false))
      )
      .toBeTruthy();
  });

  test('create FAB opens a dialog and can create an item', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace('mobile-create'));
    const item = generateItem(workspace.id, 'mobile-create');

    await page.goto('/m');
    await page.waitForLoadState('networkidle');

    await page.getByTestId('mobile-create-fab').click();
    await expect(page.getByTestId('mobile-create-dialog')).toBeVisible();
    await expect(page.getByTestId('create-title')).toBeVisible();

    await page.getByTestId('create-workspace').selectOption(String(workspace.id));
    await page.getByTestId('create-title').fill(item.title);
    await expect(page.getByTestId('create-type')).not.toHaveValue('');
    await page.getByTestId('create-submit').click();
    await expect(page.getByTestId('mobile-create-dialog')).not.toBeVisible();
    await expect(page).toHaveURL(/\/m\/items\/\d+/);
    await expect(page.getByTestId('detail-title')).toHaveText(item.title);
  });

  test('expanded optional fields remain usable above the bottom navigation', async ({
    page,
    request,
  }) => {
    const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`mobile-optional-${suffix}`)
    );
    const item = generateItem(workspace.id, `mobile-optional-${suffix}`);
    const configuration = await configureOptionalCreateFields(request, workspace.id, suffix);

    try {
      await page.goto('/m');
      await page.waitForLoadState('networkidle');
      await page.getByTestId('mobile-create-fab').click();
      await page.getByTestId('create-workspace').selectOption(String(workspace.id));
      await page.getByTestId('create-title').fill(item.title);

      const optionalToggle = page.getByTestId('create-optional-toggle');
      await expect(optionalToggle).toBeVisible();
      await optionalToggle.click();
      await expect(page.getByTestId('configured-system-end_date')).toBeVisible();

      const submit = page.getByTestId('create-submit');
      await submit.scrollIntoViewIfNeeded();
      const bottomEdgeBelongsToSubmit = await submit.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        const topmost = document.elementFromPoint(rect.left + rect.width / 2, rect.bottom - 2);
        return topmost === element || element.contains(topmost);
      });
      expect(bottomEdgeBelongsToSubmit).toBe(true);

      await submit.click();
      await expect(page.getByTestId('mobile-create-dialog')).not.toBeVisible();
      await expect(page).toHaveURL(/\/m\/items\/\d+/);
      await expect(page.getByTestId('detail-title')).toHaveText(item.title);
    } finally {
      await request
        .delete(`/api/configuration-sets/${configuration.configurationId}`, {
          headers: SEC_FETCH,
        })
        .catch(() => {});
      await request
        .delete(`/api/screens/${configuration.screenId}`, {
          headers: SEC_FETCH,
        })
        .catch(() => {});
    }
  });
});
