import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  API_BASE: '/api',
  fetchAPI: vi.fn(),
  fetchAPIV2: vi.fn(),
  fetchV2Data: vi.fn(),
  fetchAllV2Pages: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAllV2Pages, fetchAPIV2, fetchV2Data } = await import('./core.js');
const { attachments, labels } = await import('./misc.js');
const { userPreferences } = await import('./users.js');
const { recurrence } = await import('./recurrence.js');
const { getDiagrams, updateDiagram } = await import('./misc.js');
const { pageLabels, pages } = await import('./pages.js');
const { time } = await import('./time.js');
const { groups } = await import('./permissions.js');
const { getAdminUsers, updateUser } = await import('./users.js');

beforeEach(() => {
  fetchAPIV2.mockReset();
  fetchV2Data.mockReset();
  fetchAllV2Pages.mockReset();
});

describe('v2 labels', () => {
  it('moves workspace context into the canonical create path', async () => {
    await labels.create({ workspace_id: 7, name: 'Backend', color: '#123456' });

    expect(fetchV2Data).toHaveBeenCalledWith('/workspaces/7/labels', {
      method: 'POST',
      body: JSON.stringify({ name: 'Backend', color: '#123456' }),
    });
  });

  it('uses merge patch for label updates', async () => {
    await labels.update(7, 9, { color: '#abcdef' });

    expect(fetchV2Data).toHaveBeenCalledWith('/workspaces/7/labels/9', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ color: '#abcdef' }),
    });
  });
});

describe('v2 preferences', () => {
  it('uses the shared owner document and merge patch', async () => {
    await userPreferences.get();
    await userPreferences.update({ color_mode: 'dark' });

    expect(fetchV2Data).toHaveBeenNthCalledWith(1, '/users/me/preferences', {});
    expect(fetchV2Data).toHaveBeenNthCalledWith(2, '/users/me/preferences', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ color_mode: 'dark' }),
    });
  });
});

describe('v2 recurrence', () => {
  it('uses merge patch and canonical page parameters', async () => {
    await recurrence.update(12, { dtend: null });
    await recurrence.getInstances(12, { page: 2, page_size: 20 });

    expect(fetchV2Data).toHaveBeenNthCalledWith(1, '/items/12/recurrence', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ dtend: null }),
    });
    expect(fetchAPIV2).toHaveBeenCalledWith('/items/12/recurrence/instances?page=2&page_size=20');
  });
});

describe('v2 item diagrams', () => {
  it('uses canonical detail paths and merge patch', async () => {
    await getDiagrams(12);
    await updateDiagram(4, 'Flow', '{}');

    expect(fetchV2Data).toHaveBeenNthCalledWith(1, '/items/12/diagrams', {});
    expect(fetchV2Data).toHaveBeenNthCalledWith(2, '/item-diagrams/4', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ name: 'Flow', diagram_data: '{}' }),
    });
  });
});

describe('v2 item attachments', () => {
  it('uses canonical pagination and preserves request options', async () => {
    const controller = new AbortController();

    await attachments.getByItem(12, { page: 2, limit: 20, signal: controller.signal });

    expect(fetchAPIV2).toHaveBeenCalledWith('/items/12/attachments?page=2&page_size=20', {
      signal: controller.signal,
    });
  });

  it('uploads item files through the canonical owner route', async () => {
    const file = new File(['hello'], 'notes.txt', { type: 'text/plain' });

    await attachments.uploadToItem(12, file);

    expect(fetchV2Data).toHaveBeenCalledWith('/items/12/attachments', {
      method: 'POST',
      body: expect.any(FormData),
    });
    expect(fetchV2Data.mock.calls[0][1].body.get('file')).toBe(file);
  });

  it('uses v2 binary URLs and deletion', async () => {
    expect(attachments.getContentUrl(7)).toBe('/api/v2/attachments/7/content');
    expect(attachments.getDownloadUrl(7)).toBe('/api/v2/attachments/7/download');
    expect(attachments.getThumbnailUrl(7)).toBe('/api/v2/attachments/7/thumbnail');

    await attachments.delete(7);

    expect(fetchV2Data).toHaveBeenCalledWith('/attachments/7', { method: 'DELETE' });
  });
});

describe('v2 page diagrams', () => {
  it('uses the workspace-owned detail path and merge patch', async () => {
    await pages.updateDiagram(7, 8, 9, { name: 'Architecture', mermaid: 'graph TD' });

    expect(fetchV2Data).toHaveBeenCalledWith('/workspaces/7/pages/8/diagrams/9', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ name: 'Architecture', mermaid: 'graph TD' }),
    });
  });
});

describe('v2 page labels', () => {
  it('uses merge patch for label updates and canonical relationship paths', async () => {
    await pageLabels.update(7, 8, { name: 'Docs', color: '#abcdef' });
    await pageLabels.addToPage(7, 9, 8);

    expect(fetchV2Data).toHaveBeenNthCalledWith(1, '/workspaces/7/page-labels/8', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ name: 'Docs', color: '#abcdef' }),
    });
    expect(fetchV2Data).toHaveBeenNthCalledWith(2, '/workspaces/7/pages/9/labels', {
      method: 'POST',
      body: JSON.stringify({ label_id: 8 }),
    });
  });
});

describe('v2 worklogs', () => {
  it('uses canonical date filters and merge patch', async () => {
    await time.worklogs.getAll({ date_from: '2026-09-01', date_to: '2026-09-02' });
    await time.worklogs.update(10, { description: 'Review' });

    expect(fetchAllV2Pages).toHaveBeenCalledWith('/time/worklogs?from=2026-09-01&to=2026-09-02', {});
    expect(fetchV2Data).toHaveBeenCalledWith('/time/worklogs/10', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ description: 'Review' }),
    });
  });

  it('loads every item worklog page and preserves cancellation', async () => {
    const controller = new AbortController();

    await time.worklogs.getByItem(42, { signal: controller.signal });

    expect(fetchAllV2Pages).toHaveBeenCalledWith('/items/42/worklogs', {
      signal: controller.signal,
    });
    expect(fetchV2Data).not.toHaveBeenCalled();
  });
});

describe('v2 administration', () => {
  it('uses canonical group pages and merge patch', async () => {
    fetchV2Data.mockResolvedValueOnce({ member_ids: [3] });

    await groups.getAdminAll();
    await groups.addMembers(2, [4]);

    expect(fetchAllV2Pages).toHaveBeenCalledWith('/admin/groups');
    expect(fetchV2Data).toHaveBeenNthCalledWith(1, '/admin/groups/2');
    expect(fetchV2Data).toHaveBeenNthCalledWith(2, '/admin/groups/2', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ member_ids: [3, 4] }),
    });
  });

  it('uses canonical admin user pages and merge patch', async () => {
    await getAdminUsers();
    await updateUser(5, { first_name: 'Ada' });

    expect(fetchAllV2Pages).toHaveBeenCalledWith('/admin/users');
    expect(fetchV2Data).toHaveBeenCalledWith('/admin/users/5', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ first_name: 'Ada' }),
    });
  });
});
