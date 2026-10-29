import { expect, test } from '../fixtures/mail';
import type { APIRequestContext } from '@playwright/test';
import { authenticateAdminRequest } from '../fixtures/api-helpers';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createSimpleRequestType,
} from '../helpers/portal-setup';

/**
 * Portal ticket ↔ knowledge-base article links (WI-1134).
 *
 * An agent inserts a knowledge-page link into a reply on a portal request
 * via the composer's page-link picker; the portal customer sees the reply
 * rendered as an in-portal article link, opens it, and lands on the KB
 * article view. Pages outside the portal's KB wiring stay plain text.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function createWorkspacePageViaAPI(
  request: APIRequestContext,
  workspaceId: number,
  title: string,
  content: string,
  parentId?: number,
): Promise<number> {
  const response = await request.post(`${BASE_URL}/api/v2/workspaces/${workspaceId}/pages`, {
    headers: SEC_FETCH,
    data: { title, content, ...(parentId ? { parent_id: parentId } : {}) },
  });
  expect(response.ok(), `create page: ${response.status()} ${await response.text()}`).toBeTruthy();
  const body = await response.json();
  return body.data.id as number;
}

async function wireKnowledgeBaseSources(
  request: APIRequestContext,
  channelId: number,
  sources: Array<{ workspace_id: number; root_page_id?: number }>,
): Promise<void> {
  const resp = await request.put(`${BASE_URL}/api/channels/${channelId}/config`, {
    headers: SEC_FETCH,
    data: { config: { knowledge_base_page_sources: sources } },
  });
  expect(resp.ok(), `wire KB sources: ${resp.status()} ${await resp.text()}`).toBeTruthy();
}

/**
 * Signs a portal customer in via magic link inside the given browser context
 * and returns the customer email. The context must start unauthenticated.
 */
async function signInPortalCustomer(
  page: import('@playwright/test').Page,
  slug: string,
  stamp: number,
  mail: { waitForLast(opts: { to: string; subject: string; since: Date; timeoutMs?: number }): Promise<{ Text: string }> },
): Promise<string> {
  const customerEmail = `e2e-kblink-${stamp}@windshift.test`;
  const since = new Date();
  const reqResp = await page.request.post(`/api/portal/${slug}/auth/request`, {
    headers: SEC_FETCH,
    data: { email: customerEmail },
  });
  expect(reqResp.ok(), `magic-link request: ${reqResp.status()}`).toBeTruthy();
  const msg = await mail.waitForLast({
    to: customerEmail,
    subject: 'Sign in to your portal',
    since,
    timeoutMs: 5000,
  });
  const token = msg.Text.match(/[?#&]token=([A-Za-z0-9_=-]+)/)![1];
  const verifyResp = await page.request.get(
    `/api/portal/${slug}/auth/verify?token=${encodeURIComponent(token)}`,
    { headers: SEC_FETCH },
  );
  expect(verifyResp.ok(), 'verify magic link').toBeTruthy();
  return customerEmail;
}

test.describe('Portal ticket KB article links', () => {
  test('agent inserts KB link into reply; customer opens the article in-portal', async ({
    page,
    request,
    mail,
    browser,
  }) => {
    mail.skipIfMissing();
    await authenticateAdminRequest(request);
    const stamp = Date.now();
    const slug = `e2e-kblink-${stamp}`;

    // Setup: portal channel with a request type, and a KB wiring limited to
    // the handbook subtree so pages outside it stay unpublished.
    const channel = await createPortalChannel(request, {
      slug,
      name: `KB Link ${stamp}`,
    });
    const rt = await createSimpleRequestType(request, channel, {
      name: 'General request',
    });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    const handbookId = await createWorkspacePageViaAPI(
      request,
      channel.workspaceId,
      'Handbook',
      'Root of the published handbook tree.',
    );
    const guideId = await createWorkspacePageViaAPI(
      request,
      channel.workspaceId,
      'Fandorin setup guide',
      'Step by step setup instructions.',
      handbookId,
    );
    const roadmapId = await createWorkspacePageViaAPI(
      request,
      channel.workspaceId,
      'Internal roadmap',
      'Internal-only roadmap notes.',
    );
    await wireKnowledgeBaseSources(request, channel.channelId, [
      { workspace_id: channel.workspaceId, root_page_id: handbookId },
    ]);

    // Customer: sign in and submit the request under test (setup via API).
    const customerContext = await browser.newContext({
      storageState: { cookies: [], origins: [] },
    });
    const customerPage = await customerContext.newPage();
    const customerEmail = await signInPortalCustomer(customerPage, slug, stamp, mail);
    expect(customerEmail).toContain('@windshift.test');
    const submitResp = await customerPage.request.post(`/api/portal/${slug}/submit`, {
      headers: SEC_FETCH,
      data: {
        title: `Printer on fire ${stamp}`,
        description: 'The office printer is emitting smoke.',
      },
    });
    expect(submitResp.ok(), `portal submit: ${submitResp.status()} ${await submitResp.text()}`).toBeTruthy();
    const { item_id: itemId } = (await submitResp.json()) as { item_id: number };

    // Agent: open the request in the internal UI and insert the KB link
    // through the composer's page-link picker.
    await page.goto(`/workspaces/${channel.workspaceId}/items/${itemId}`);
    await page.getByTestId('comment-editor').click();
    const insertButton = page.getByTestId('milkdown-insert-page-link');
    await expect(insertButton).toBeVisible();
    await insertButton.click();
    await page.getByTestId('page-link-search').fill('Fandorin setup');
    const option = page.getByTestId('page-link-option').first();
    await expect(option).toBeVisible();
    await option.click();

    const submitComment = page.getByTestId('comment-submit');
    await expect(submitComment).toBeEnabled();
    await submitComment.click();
    const comment = page
      .getByTestId('comment-item')
      .filter({ has: page.locator(`a[href="page:${guideId}"]`) });
    await expect(comment).toHaveCount(1);
    await expect(comment).toContainText('Fandorin setup guide');

    // Customer: open the conversation and follow the article link.
    await customerPage.goto(`${BASE_URL}/portal/${slug}?view=requests&id=${itemId}`);
    const articleLink = customerPage.locator(`a[href*="/portal/${slug}/kb/${guideId}"]`);
    await expect(articleLink).toHaveCount(1);
    await expect(articleLink).toContainText('Fandorin setup guide');

    await Promise.all([
      customerPage.waitForURL(new RegExp(`/portal/${slug}/kb/${guideId}`)),
      articleLink.click(),
    ]);
    await expect(customerPage.getByTestId('portal-kb-article-title')).toContainText(
      'Fandorin setup guide',
    );
    await expect(customerPage.getByTestId('portal-kb-article')).toBeVisible();

    // The unpublished roadmap page must not appear as a link anywhere in the
    // conversation — the customer sees only pages this portal publishes.
    await customerPage.goBack();
    await expect(customerPage.getByTestId('portal-request-detail-title')).toBeVisible();
    await expect(customerPage.locator(`a[href*="kb/${roadmapId}"]`)).toHaveCount(0);
    await customerContext.close();
  });
});
