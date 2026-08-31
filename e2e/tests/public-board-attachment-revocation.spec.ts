import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

test('public attachment bytes are revalidated after every board revocation path', async ({
  request,
  browser,
}) => {
  const stamp = Date.now();
  const title = `e2e-public-attachment-${stamp}`;
  const oldSlug = `e2e-public-attachment-${stamp}-old`;
  const newSlug = `e2e-public-attachment-${stamp}-new`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(`pub-att-${stamp}`));
  const item = await createItemViaAPI(request, workspace.id, { title });

  const png = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=',
    'base64'
  );
  const uploadResponse = await request.post('/api/attachments/upload', {
    headers: SEC_FETCH,
    multipart: {
      entity_id: String(item.id),
      entity_type: 'item',
      file: {
        name: 'revocation-probe.png',
        mimeType: 'image/png',
        buffer: png,
      },
    },
  });
  expect(uploadResponse.ok(), `upload attachment: ${await uploadResponse.text()}`).toBeTruthy();
  const upload = await uploadResponse.json();
  const attachmentID = upload.id ?? upload.attachment?.id ?? upload.attachment_id;
  expect(attachmentID).toBeGreaterThan(0);

  const includeQuery = `workspaceKey = "${workspace.key}" AND title ~ "${title}"`;
  const collectionResponse = await request.post('/api/collections', {
    headers: SEC_FETCH,
    data: {
      name: `Public attachment revocation ${stamp}`,
      description: 'Browser-cache revocation fixture',
      ql_query: includeQuery,
      workspace_id: workspace.id,
      is_public: true,
      public_slug: oldSlug,
    },
  });
  expect(collectionResponse.status(), `create collection: ${await collectionResponse.text()}`).toBe(
    201
  );
  const collection = await collectionResponse.json();

  const context = await browser.newContext({
    storageState: { cookies: [], origins: [] },
  });
  const page = await context.newPage();

  const probeInBrowser = async (slug: string, expectedStatus: number, shouldRender: boolean) => {
    const url = `${BASE_URL}/api/public/board/${slug}/attachments/${attachmentID}/download`;
    // Leave the resource before returning to the exact same URL. This keeps
    // Chromium cache state but avoids the decoded-image reuse that can happen
    // when an img element is merely removed and recreated in one document.
    await page.goto('about:blank');
    const response = await page.goto(url, { waitUntil: 'commit' });
    expect(response, `browser navigation response for ${slug}`).not.toBeNull();
    if (!response) throw new Error(`browser navigation response missing for ${slug}`);
    await page.waitForLoadState('load');
    expect(response.status(), `browser response for ${slug}`).toBe(expectedStatus);
    const contentType = response.headers()['content-type'] || '';
    expect(contentType.startsWith('image/'), `rendered image for ${slug}`).toBe(shouldRender);
    expect(response.headers()['cache-control']).toContain('no-store');
  };

  // Prime the browser with the exact URL that will be retained through each
  // mutation. A stale public,max-age response would keep rendering here.
  await probeInBrowser(oldSlug, 200, true);

  const rotateResponse = await request.put(`/api/collections/${collection.id}/public`, {
    headers: SEC_FETCH,
    data: { is_public: true, public_slug: newSlug },
  });
  expect(rotateResponse.ok(), `rotate slug: ${await rotateResponse.text()}`).toBeTruthy();
  await probeInBrowser(oldSlug, 404, false);
  await probeInBrowser(newSlug, 200, true);

  const removeResponse = await request.put(`/api/collections/${collection.id}`, {
    headers: SEC_FETCH,
    data: {
      name: collection.name,
      description: collection.description,
      ql_query: `workspaceKey = "${workspace.key}" AND title ~ "not-${stamp}"`,
    },
  });
  expect(
    removeResponse.ok(),
    `remove item from query: ${await removeResponse.text()}`
  ).toBeTruthy();
  await probeInBrowser(newSlug, 404, false);

  const restoreResponse = await request.put(`/api/collections/${collection.id}`, {
    headers: SEC_FETCH,
    data: {
      name: collection.name,
      description: collection.description,
      ql_query: includeQuery,
    },
  });
  expect(
    restoreResponse.ok(),
    `restore item to query: ${await restoreResponse.text()}`
  ).toBeTruthy();
  await probeInBrowser(newSlug, 200, true);

  const unpublishResponse = await request.put(`/api/collections/${collection.id}/public`, {
    headers: SEC_FETCH,
    data: { is_public: false, public_slug: newSlug },
  });
  expect(unpublishResponse.ok(), `unpublish board: ${await unpublishResponse.text()}`).toBeTruthy();
  await probeInBrowser(newSlug, 404, false);

  await context.close();
});
