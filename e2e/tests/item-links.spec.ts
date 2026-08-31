import {
  createItemViaAPI,
  createLinkViaAPI,
  createWorkspaceViaAPI,
  listLinksForItemViaAPI,
  listLinkTypesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';
import { ItemLinksPage } from '../pages/item-links.page';

/**
 * Item linking — e2e coverage for the LinkItemModal UI and the underlying
 * /api/links surface. All scenarios run as the seeded admin user so we
 * exercise the happy-path + validation layers; permission-boundary tests
 * (alice without W1 access) would need a second authenticated context and
 * live outside this file.
 */

test.describe('Item linking', () => {
  let itemPage: ItemPage;
  let linksPage: ItemLinksPage;
  let workspaceId: string;
  let workspaceNumericId: number;
  let relatesToLinkTypeId: number;

  test.beforeAll(async ({ request }) => {
    const linkTypes = await listLinkTypesViaAPI(request);
    const relatesTo = linkTypes.find((lt) => lt.name === 'Relates To');
    if (!relatesTo) {
      throw new Error('Default "Relates To" link type was not seeded');
    }
    relatesToLinkTypeId = relatesTo.id;
  });

  test.beforeEach(async ({ page, request }) => {
    itemPage = new ItemPage(page);
    linksPage = new ItemLinksPage(page);

    const testWorkspace = generateWorkspace();
    const created = await createWorkspaceViaAPI(request, {
      name: testWorkspace.name,
      key: testWorkspace.key,
      description: testWorkspace.description,
    });
    workspaceNumericId = created.id;
    workspaceId = String(created.id);
  });

  test('creates a link via the modal and renders it in the detail view', async ({
    page,
    request,
  }) => {
    const source = generateItem(0, 'src');
    const target = generateItem(0, 'tgt');
    const srcItem = await createItemViaAPI(request, workspaceNumericId, {
      title: source.title,
    });
    await createItemViaAPI(request, workspaceNumericId, { title: target.title });

    await itemPage.gotoWorkspaceBacklog(workspaceId);
    await itemPage.openItemDetailModal(source.title);

    await linksPage.openLinkModal();
    await linksPage.selectLinkType('Relates To');
    // Search the full unique title so prior tests' items in the same
    // DB don't shadow the intended target.
    await linksPage.searchAndSelect(target.title);
    await linksPage.submitLink();

    await linksPage.expectLinkVisible(target.title);

    // Confirm the link landed on the server, not just the UI cache.
    const links = await listLinksForItemViaAPI(request, srcItem.id);
    expect(links.outgoing).toHaveLength(1);
    expect(links.outgoing[0].target_title).toBe(target.title);
  });

  test('deletes a link via the row hover button', async ({ page, request }) => {
    const source = generateItem(0, 'src-del');
    const target = generateItem(0, 'tgt-del');
    const srcItem = await createItemViaAPI(request, workspaceNumericId, {
      title: source.title,
    });
    const tgtItem = await createItemViaAPI(request, workspaceNumericId, {
      title: target.title,
    });

    await createLinkViaAPI(request, {
      link_type_id: relatesToLinkTypeId,
      source_type: 'item',
      source_id: srcItem.id,
      target_type: 'item',
      target_id: tgtItem.id,
    });

    await itemPage.gotoWorkspaceBacklog(workspaceId);
    await itemPage.openItemDetailModal(source.title);
    await linksPage.expectLinkVisible(target.title);

    await linksPage.deleteLink(target.title);

    const links = await listLinksForItemViaAPI(request, srcItem.id);
    expect(links.outgoing).toEqual([]);
  });

  test('rejects a duplicate link between the same pair', async ({ page, request }) => {
    const source = generateItem(0, 'src-dup');
    const target = generateItem(0, 'tgt-dup');
    const srcItem = await createItemViaAPI(request, workspaceNumericId, {
      title: source.title,
    });
    const tgtItem = await createItemViaAPI(request, workspaceNumericId, {
      title: target.title,
    });

    // Seed the first link via the API, then try to create a duplicate via the
    // UI — the backend should respond 409 and the second link should never
    // land in the list.
    await createLinkViaAPI(request, {
      link_type_id: relatesToLinkTypeId,
      source_type: 'item',
      source_id: srcItem.id,
      target_type: 'item',
      target_id: tgtItem.id,
    });

    await itemPage.gotoWorkspaceBacklog(workspaceId);
    await itemPage.openItemDetailModal(source.title);

    await linksPage.openLinkModal();
    await linksPage.selectLinkType('Relates To');
    await linksPage.searchAndSelect(target.title);

    const postResponsePromise = page.waitForResponse(
      (r) => r.url().endsWith('/api/links') && r.request().method() === 'POST'
    );
    await page.getByTestId('link-modal').getByRole('button', { name: 'Add Link' }).click();
    const postResponse = await postResponsePromise;
    expect(postResponse.status()).toBe(409);

    // Server still holds exactly one link — the duplicate never persisted.
    const links = await listLinksForItemViaAPI(request, srcItem.id);
    expect(links.outgoing).toHaveLength(1);
  });

  test('search filters by link type (work items for "Relates To")', async ({ page, request }) => {
    // Per-run token so the search query can't be polluted by items left
    // behind by previous test runs / Playwright retries — admin sees items
    // across every workspace.
    const token = `findme${Math.random().toString(36).slice(2, 8)}`;
    const source = generateItem(0, 'src-search');
    const peer1 = generateItem(0, `peer1-${token}`);
    const peer2 = generateItem(0, `peer2-${token}`);

    await createItemViaAPI(request, workspaceNumericId, { title: source.title });
    await createItemViaAPI(request, workspaceNumericId, { title: peer1.title });
    await createItemViaAPI(request, workspaceNumericId, { title: peer2.title });

    await itemPage.gotoWorkspaceBacklog(workspaceId);
    await itemPage.openItemDetailModal(source.title);

    await linksPage.openLinkModal();
    await linksPage.selectLinkType('Relates To');

    // Token is present in both peer titles but not the source — a
    // non-Tests link type searches items only, so both peers and neither
    // test_cases should appear.
    const count = await linksPage.searchResultCount(token);
    expect(count).toBe(2);
  });
});
