import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { pages } from './pages.js';

/**
 * Pin the URL / method / body shape of each `pages.*` client method.
 * fetchAPI is invoked through the global `fetch`; we stub `fetch` and
 * assert what got sent. Catching a path or verb mismatch here surfaces a
 * server-vs-client wiring drift before it lands in production.
 *
 * Routes mirror `internal/handlers/router.go` and `frontend/src/lib/api/core.js`
 * (which prefixes `/api`).
 */

describe('pages API client', () => {
  let fetchSpy;

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: {
          'Content-Type': 'application/json',
          Date: new Date().toUTCString(),
        },
      })
    );
    vi.stubGlobal('fetch', fetchSpy);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const lastCall = () => fetchSpy.mock.calls[0];

  test('getTree → GET /api/workspaces/:id/pages/tree', async () => {
    await pages.getTree(42);
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/tree');
    expect(init.method).toBeUndefined(); // fetchAPI defaults to GET
    expect(init.credentials).toBe('same-origin');
  });

  test('getPage → GET /api/workspaces/:id/pages/:pageId', async () => {
    await pages.getPage(42, 7);
    const [url] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7');
  });

  test('createPage → POST with body, optional parentId and isHome', async () => {
    await pages.createPage(42, {
      title: 'New',
      content: 'body',
      parentId: 5,
      isHome: true,
    });
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({
      title: 'New',
      content: 'body',
      parent_id: 5,
      is_home: true,
      metadata: {},
    });
  });

  test('createPage defaults parentId=null and isHome=false', async () => {
    await pages.createPage(42, { title: 'New' });
    const [, init] = lastCall();
    const body = JSON.parse(init.body);
    expect(body.parent_id).toBeNull();
    expect(body.is_home).toBe(false);
    expect(body.content).toBe('');
    expect(body.metadata).toEqual({});
  });

  test('updatePage → PUT with title and content only', async () => {
    await pages.updatePage(42, 7, { title: 'Edited', content: 'rewritten' });
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body)).toEqual({
      title: 'Edited',
      content: 'rewritten',
    });
  });

  test('updatePage forwards the optional content-hash precondition', async () => {
    await pages.updatePage(42, 7, {
      title: 'Edited',
      content: 'rewritten',
      expectedContentHash: 'hash-before-edit',
    });
    const [, init] = lastCall();
    expect(JSON.parse(init.body)).toEqual({
      title: 'Edited',
      content: 'rewritten',
      expected_content_hash: 'hash-before-edit',
    });
  });

  test('archivePage → DELETE /api/workspaces/:id/pages/:pageId', async () => {
    await pages.archivePage(42, 7);
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7');
    expect(init.method).toBe('DELETE');
  });

  test('movePage → POST /api/workspaces/:id/pages/:pageId/move with parent_id', async () => {
    await pages.movePage(42, 7, 11);
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/move');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({
      parent_id: 11,
      prev_sibling_id: null,
      next_sibling_id: null,
    });
  });

  test('movePage to root passes parent_id: null', async () => {
    await pages.movePage(42, 7, null);
    const [, init] = lastCall();
    expect(JSON.parse(init.body)).toEqual({
      parent_id: null,
      prev_sibling_id: null,
      next_sibling_id: null,
    });
  });

  test('movePage forwards prev/next sibling positioning for reorder', async () => {
    await pages.movePage(42, 7, 11, { prevSiblingId: 3, nextSiblingId: 5 });
    const [, init] = lastCall();
    expect(JSON.parse(init.body)).toEqual({
      parent_id: 11,
      prev_sibling_id: 3,
      next_sibling_id: 5,
    });
  });

  test('movePage forwards a destination workspace for cross-workspace moves', async () => {
    await pages.movePage(42, 7, null, { destinationWorkspaceId: 99 });
    const [, init] = lastCall();
    expect(JSON.parse(init.body)).toEqual({
      destination_workspace_id: 99,
      parent_id: null,
      prev_sibling_id: null,
      next_sibling_id: null,
    });
  });

  test('getHistory → GET with default limit/offset query string', async () => {
    await pages.getHistory(42, 7);
    const [url] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/history?limit=50&offset=0');
  });

  test('getHistory honors custom pagination', async () => {
    await pages.getHistory(42, 7, { limit: 10, offset: 20 });
    const [url] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/history?limit=10&offset=20');
  });

  test('getRevision → GET /api/workspaces/:id/pages/:pageId/history/:revId', async () => {
    await pages.getRevision(42, 7, 99);
    const [url] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/history/99');
  });

  test('restoreRevision → POST /api/workspaces/:id/pages/:pageId/history/:revId/restore', async () => {
    await pages.restoreRevision(42, 7, 99);
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/history/99/restore');
    expect(init.method).toBe('POST');
  });

  test('createDiagram → POST with scene, placement, and content hash', async () => {
    const scene = { elements: [], appState: {}, files: {} };
    await pages.createDiagram(42, 7, {
      name: 'Flow',
      excalidraw: scene,
      placement: 'end',
      expectedContentHash: 'hash-1',
    });
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/diagrams');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({
      name: 'Flow',
      excalidraw: scene,
      placement: 'end',
      expected_content_hash: 'hash-1',
    });
  });

  test('updateDiagram → PUT with replacement name, scene, and content hash', async () => {
    const scene = {
      elements: [{ id: 'one', type: 'rectangle' }],
      appState: {},
      files: {},
    };
    await pages.updateDiagram(42, 7, 91, {
      name: 'Updated flow',
      excalidraw: scene,
      expectedContentHash: 'hash-2',
    });
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/diagrams/91');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body)).toEqual({
      name: 'Updated flow',
      excalidraw: scene,
      expected_content_hash: 'hash-2',
    });
  });

  test('getPermissions → GET /api/workspaces/:id/pages/:pageId/permissions', async () => {
    await pages.getPermissions(42, 7);
    const [url] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/permissions');
  });

  test('grantPermission → POST /api/workspaces/:id/pages/:pageId/permissions with snake_cased body', async () => {
    await pages.grantPermission(42, 7, {
      principalType: 'user',
      principalId: 8,
      permissionLevel: 'edit',
    });
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/permissions');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({
      principal_type: 'user',
      principal_id: 8,
      permission_level: 'edit',
    });
  });

  test('revokePermission → DELETE /api/workspaces/:id/pages/:pageId/permissions/:permId', async () => {
    await pages.revokePermission(42, 7, 99);
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/permissions/99');
    expect(init.method).toBe('DELETE');
  });

  test('setInheritance → PATCH /api/workspaces/:id/pages/:pageId/inheritance with inherit_permissions body', async () => {
    await pages.setInheritance(42, 7, false);
    const [url, init] = lastCall();
    expect(url).toBe('/api/workspaces/42/pages/7/inheritance');
    expect(init.method).toBe('PATCH');
    expect(JSON.parse(init.body)).toEqual({ inherit_permissions: false });
  });

  test('searchKnowledge → GET /api/workspaces/:id/knowledge/search with q and limit', async () => {
    await pages.searchKnowledge(42, 'onboarding', { limit: 10 });
    const [url] = lastCall();
    expect(url).toBe('/api/workspaces/42/knowledge/search?q=onboarding&limit=10');
  });

  test('searchKnowledge applies the default limit when none is passed', async () => {
    await pages.searchKnowledge(42, 'onboarding');
    const [url] = lastCall();
    expect(url).toBe('/api/workspaces/42/knowledge/search?q=onboarding&limit=25');
  });
});
