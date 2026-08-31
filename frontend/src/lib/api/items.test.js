import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * Pin that the mutating `items.*` API methods broadcast a cross-tab freshness
 * notice to other open tabs on success (and only on success). The notice
 * itself is exercised by crossTabSync.test.js; here we only assert that the
 * wrapper wires `notifyItemMutation` into the post-success path and does not
 * alter the request/response shape.
 */

class FakeBroadcastChannel {
  static instances = new Set();
  #listeners = new Set();
  constructor() {
    FakeBroadcastChannel.instances.add(this);
  }
  postMessage(data) {
    for (const ch of FakeBroadcastChannel.instances) {
      if (ch === this) continue;
      for (const cb of ch.#listeners) cb({ data });
    }
  }
  addEventListener(_t, cb) {
    this.#listeners.add(cb);
  }
  removeEventListener(_t, cb) {
    this.#listeners.delete(cb);
  }
  close() {
    this.#listeners.clear();
    FakeBroadcastChannel.instances.delete(this);
  }
}

let posted = [];

describe('items API cross-tab broadcast', () => {
  let fetchSpy;

  beforeEach(() => {
    vi.resetModules();
    FakeBroadcastChannel.instances.clear();
    posted = [];
    vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel);

    fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: 42, title: 'x' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json', Date: new Date().toUTCString() },
      })
    );
    vi.stubGlobal('fetch', fetchSpy);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // Create a peer channel that captures everything the items client broadcasts.
  async function captureBroadcasts() {
    const mod = await import('./items.js');
    const peer = new FakeBroadcastChannel();
    peer.addEventListener('message', (e) => posted.push(e.data));
    return { mod, peer };
  }

  it('create broadcasts a notice with the new item id from the response', async () => {
    const { mod, peer } = await captureBroadcasts();
    const result = await mod.items.create({ title: 'x' });
    expect(result.id).toBe(42);
    expect(posted).toEqual([expect.objectContaining({ type: 'create', itemId: 42 })]);
    peer.close();
  });

  it('update broadcasts with the id arg', async () => {
    const { mod, peer } = await captureBroadcasts();
    await mod.items.update(7, { title: 'y' });
    expect(posted[0]).toEqual(expect.objectContaining({ type: 'update', itemId: 7 }));
    peer.close();
  });

  it('transition broadcasts with the id arg', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ item: { id: 9, status_id: 2 } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json', Date: new Date().toUTCString() },
      })
    );
    const { mod, peer } = await captureBroadcasts();
    const item = await mod.items.transition(9, 2);
    expect(item.id).toBe(9);
    expect(posted[0]).toEqual(expect.objectContaining({ type: 'transition', itemId: 9 }));
    peer.close();
  });

  it('updateFracIndex broadcasts a reorder notice', async () => {
    const { mod, peer } = await captureBroadcasts();
    await mod.items.updateFracIndex(3, { frac_index: 'a0' });
    expect(posted[0]).toEqual(expect.objectContaining({ type: 'reorder', itemId: 3 }));
    peer.close();
  });

  it('previews a cross-workspace move without broadcasting a mutation', async () => {
    const { mod, peer } = await captureBroadcasts();
    await mod.items.previewWorkspaceMove(7, { destination_workspace_id: 9 });

    expect(fetchSpy.mock.calls[0][0]).toBe('/api/items/7/move-workspace/preview');
    expect(fetchSpy.mock.calls[0][1].method).toBe('POST');
    expect(JSON.parse(fetchSpy.mock.calls[0][1].body)).toEqual({ destination_workspace_id: 9 });
    expect(posted).toEqual([]);
    peer.close();
  });

  it('moves an item between workspaces and broadcasts the item id', async () => {
    const { mod, peer } = await captureBroadcasts();
    const payload = {
      destination_workspace_id: 9,
      target_item_type_id: 2,
      target_status_id: 3,
      target_priority_id: null,
    };
    await mod.items.moveWorkspace(7, payload);

    expect(fetchSpy.mock.calls[0][0]).toBe('/api/items/7/move-workspace');
    expect(fetchSpy.mock.calls[0][1].method).toBe('POST');
    expect(JSON.parse(fetchSpy.mock.calls[0][1].body)).toEqual(payload);
    expect(posted[0]).toEqual(expect.objectContaining({ type: 'update', itemId: 7 }));
    peer.close();
  });

  it('does not broadcast when the request rejects', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'boom' }), {
        status: 500,
        headers: { 'Content-Type': 'application/json', Date: new Date().toUTCString() },
      })
    );
    const { mod, peer } = await captureBroadcasts();
    await expect(mod.items.update(1, { title: 'z' })).rejects.toBeDefined();
    expect(posted).toEqual([]);
    peer.close();
  });

  it('loads the numeric item-detail summary with the optional surface selector', async () => {
    const { items } = await import('./items.js');
    const controller = new AbortController();

    await items.getDetailSummary(42, { surface: 'mobile', signal: controller.signal });

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/items/42/detail-summary?surface=mobile');
    expect(fetchSpy.mock.calls[0][1].signal).toBe(controller.signal);
  });

  it('loads a key-addressed item-detail summary without a preliminary item request', async () => {
    const { items } = await import('./items.js');

    await items.getDetailSummaryByKey('WI', 689);

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/workspaces/WI/items/689/detail-summary');
  });

  it('resolves the first item from the filtered backlog', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          items: [{ id: 11 }],
          pagination: { page: 1, limit: 1, total: 125, total_pages: 125 },
        }),
        {
          status: 200,
          headers: {
            'Content-Type': 'application/json',
            Date: new Date().toUTCString(),
          },
        }
      )
    );
    const { items } = await import('./items.js');

    const boundary = await items.getBacklogBoundary(7, null, 'priority = high', 'start');

    expect(boundary).toEqual({ id: 11 });
    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toContain('/api/items/backlog?');
    expect(fetchSpy.mock.calls[0][0]).toContain('workspace_id=7');
    expect(fetchSpy.mock.calls[0][0]).toContain('sub_ql=priority+%3D+high');
    expect(fetchSpy.mock.calls[0][0]).toContain('page=1');
    expect(fetchSpy.mock.calls[0][0]).toContain('limit=1');
  });

  it('uses the current total to resolve the true last backlog item', async () => {
    fetchSpy
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            items: [{ id: 11 }],
            pagination: { page: 1, limit: 1, total: 125, total_pages: 125 },
          }),
          {
            status: 200,
            headers: {
              'Content-Type': 'application/json',
              Date: new Date().toUTCString(),
            },
          }
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            items: [{ id: 135 }],
            pagination: { page: 125, limit: 1, total: 125, total_pages: 125 },
          }),
          {
            status: 200,
            headers: {
              'Content-Type': 'application/json',
              Date: new Date().toUTCString(),
            },
          }
        )
      );
    const { items } = await import('./items.js');

    const boundary = await items.getBacklogBoundary(null, 23, '', 'end');

    expect(boundary).toEqual({ id: 135 });
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(fetchSpy.mock.calls[0][0]).toContain('collection_id=23');
    expect(fetchSpy.mock.calls[1][0]).toContain('collection_id=23');
    expect(fetchSpy.mock.calls[1][0]).toContain('page=125');
    expect(fetchSpy.mock.calls[1][0]).toContain('limit=1');
  });
});
