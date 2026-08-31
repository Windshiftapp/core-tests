import {
  createItemViaAPI,
  createWorkspaceViaAPI,
  listLinkTypesViaAPI,
} from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';
import { ItemLinksPage } from '../pages/item-links.page';
import { KnowledgePage } from '../pages/knowledge.page';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

/**
 * Page-link e2e coverage. Exercises the new item↔page link path through
 * the LinkItemModal UI (Page link type → PagePicker), the Pages section
 * that renders on the item detail, and the "Work items" popover on the
 * page detail (read, unlink, add). The same-workspace invariant is covered by
 * tests/e2e_security_contracts_test.go.
 */

async function createPageViaAPI(
  request: APIRequestContext,
  workspaceId: number,
  title: string,
  content = ''
): Promise<{ id: number; title: string; workspace_id: number }> {
  const response = await request.post(`${BASE_URL}/api/workspaces/${workspaceId}/pages`, {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
    data: { title, content, parent_id: null, is_home: false },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

test.describe('Item ↔ page linking', () => {
  test.describe.configure({ mode: 'serial' });

  let workspaceNumericId: number;
  let workspaceId: string;
  let pageLinkTypeId: number;

  test.beforeAll(async ({ request }) => {
    const linkTypes = await listLinkTypesViaAPI(request);
    const pageLinkType = linkTypes.find((lt) => lt.name === 'Page');
    if (!pageLinkType) {
      throw new Error('Page system link type was not seeded — migration missing?');
    }
    pageLinkTypeId = pageLinkType.id;
  });

  test.beforeEach(async ({ request }) => {
    const ws = generateWorkspace('page-links');
    const created = await createWorkspaceViaAPI(request, {
      name: ws.name,
      key: ws.key,
      description: ws.description,
    });
    workspaceNumericId = created.id;
    workspaceId = String(created.id);
  });

  test('links a page to a work item via the modal and renders the Pages section', async ({
    page,
    request,
  }) => {
    const source = generateItem(0, 'pl-src');
    const srcItem = await createItemViaAPI(request, workspaceNumericId, {
      title: source.title,
    });
    const knowledgePage = await createPageViaAPI(
      request,
      workspaceNumericId,
      `pl-page-${Date.now()}`
    );

    const itemPage = new ItemPage(page);
    const linksPage = new ItemLinksPage(page);
    await itemPage.gotoWorkspaceBacklog(workspaceId);
    await itemPage.openItemDetailModal(source.title);

    // Open the regular Add-link button (the Pages section's own Add only
    // renders when pageLinkTypeId is known to the client — exercising the
    // shared modal route covers both).
    await linksPage.openLinkModal();

    // Pick the "Page" link type. The shared selectLinkType helper waits
    // for the inline #link-target-search input that doesn't render for
    // the Page type (PagePicker replaces it) — inline the picker choice
    // here instead so we wait on the right element.
    const linkModal = page.getByTestId('link-modal');
    const linkTypePicker = linkModal.locator('#link-type-picker');
    await linkTypePicker.click();
    await page
      .getByRole('option', { name: 'Page' })
      .or(page.locator('[role="option"]').filter({ hasText: 'Page' }))
      .first()
      .click({ timeout: 5000 });
    await expect(linkTypePicker).toHaveValue('Page', { timeout: 5000 });

    // Selecting "Page" swaps the inline target search for the PagePicker —
    // typing into its input must hit /api/workspaces/{id}/pages/search.
    // Click the input first so the BasePicker combobox opens — Playwright's
    // fill() doesn't always trigger the focus/open path the melt-ui
    // combobox expects on its own.
    const pagePickerInput = page.locator('#link-target-page-picker');
    await pagePickerInput.waitFor({ state: 'visible', timeout: 5000 });
    await pagePickerInput.click();

    const searchResponse = page
      .waitForResponse(
        (res) =>
          res.request().method() === 'GET' &&
          res.url().includes(`/api/workspaces/${workspaceNumericId}/pages/search`),
        { timeout: 10000 }
      )
      .catch(() => null);
    await pagePickerInput.pressSequentially(knowledgePage.title.slice(0, 6), { delay: 30 });
    await searchResponse;

    const option = page
      .getByRole('option', { name: knowledgePage.title })
      .or(page.locator('[role="option"]').filter({ hasText: knowledgePage.title }));
    await expect(option.first()).toBeVisible({ timeout: 10000 });
    await option.first().click({ timeout: 5000 });

    await linksPage.submitLink();

    const pagesRow = page.getByTestId('linked-page-row').filter({ hasText: knowledgePage.title });
    await expect(pagesRow.first()).toBeVisible({ timeout: 10000 });

    // Server-side: the link landed with target_type=page.
    const linksResp = await request.get(`${BASE_URL}/api/items/${srcItem.id}/links`);
    expect(linksResp.ok()).toBeTruthy();
    const links = (await linksResp.json()) as {
      outgoing: Array<{ target_type: string; target_id: number }>;
    };
    expect(
      links.outgoing.some((l) => l.target_type === 'page' && l.target_id === knowledgePage.id)
    ).toBeTruthy();
  });

  test('page detail "Work items" popover lists, unlinks, and re-adds links', async ({
    page,
    request,
  }) => {
    const source = generateItem(0, 'pl-popover');
    const srcItem = await createItemViaAPI(request, workspaceNumericId, {
      title: source.title,
    });
    const knowledgePage = await createPageViaAPI(
      request,
      workspaceNumericId,
      `pl-popover-${Date.now()}`
    );

    // Seed an item↔page link via the API so the popover starts populated.
    const linkResp = await request.post(`${BASE_URL}/api/links`, {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        link_type_id: pageLinkTypeId,
        source_type: 'item',
        source_id: srcItem.id,
        target_type: 'page',
        target_id: knowledgePage.id,
      },
    });
    expect(linkResp.ok()).toBeTruthy();

    const knowledge = new KnowledgePage(page);
    await knowledge.gotoPage(workspaceId, knowledgePage.id);

    const trigger = page.getByTestId('page-work-items-trigger');
    await expect(trigger).toBeVisible({ timeout: 10000 });
    // Count badge reflects the single seeded link.
    await expect(page.getByTestId('page-work-items-count')).toHaveText('1', {
      timeout: 5000,
    });

    await trigger.click();
    const popover = page.getByTestId('page-work-items-popover');
    await expect(popover).toBeVisible({ timeout: 5000 });

    const itemRow = popover.getByTestId('page-work-items-row').filter({ hasText: source.title });
    await expect(itemRow).toBeVisible({ timeout: 5000 });

    // Unlink via the per-row trash.
    await itemRow.hover();
    await itemRow.getByTestId('page-work-items-unlink').click();
    await expect(itemRow).toHaveCount(0, { timeout: 5000 });
    // Badge disappears when count drops to zero.
    await expect(page.getByTestId('page-work-items-count')).toHaveCount(0, {
      timeout: 5000,
    });

    // Re-add via the popover's Add Work Item flow.
    await page.getByTestId('page-work-items-add').click();
    const searchInput = page.getByTestId('page-work-items-add-search');
    await expect(searchInput).toBeVisible({ timeout: 5000 });

    const searchResponsePromise = page.waitForResponse(
      (response) =>
        response.request().method() === 'GET' && response.url().includes('/api/links/search?')
    );
    await searchInput.fill(source.title);
    const searchResponse = await searchResponsePromise;
    expect(searchResponse.ok()).toBeTruthy();

    const result = page.getByTestId('page-work-items-add-result').filter({ hasText: source.title });
    await expect(result.first()).toBeVisible();
    await result.first().click();

    // Badge returns to 1 once the new link round-trips.
    await expect(page.getByTestId('page-work-items-count')).toHaveText('1', {
      timeout: 10000,
    });
  });
});
