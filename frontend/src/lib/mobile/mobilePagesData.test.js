import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api/pages.js', () => ({ pages: { getSummaries: vi.fn() } }));

import { pages } from '../api/pages.js';
import { BACKGROUND_READ } from '../api/readScheduler.js';
import {
  WORKSPACE_BATCH_SIZE,
  fetchWorkspacePageSections,
  searchPagesAcrossWorkspaces,
} from './mobilePagesData.js';

function summary(workspaceId, overrides = {}) {
  return {
    workspace_id: workspaceId,
    page_id: workspaceId * 10,
    parent_id: null,
    title: `Page ${workspaceId}`,
    depth: 0,
    is_home: false,
    updated_at: '2026-01-02T03:04:05Z',
    ...overrides,
  };
}

function workspaces(count) {
  return Array.from({ length: count }, (_, i) => ({ id: i + 1, name: `WS ${i + 1}` }));
}

describe('mobilePagesData — bounded cross-workspace page reads', () => {
  beforeEach(() => {
    pages.getSummaries.mockReset();
  });

  test('lists every workspace in batches of the server cap', async () => {
    const all = workspaces(WORKSPACE_BATCH_SIZE * 2 + 5);
    pages.getSummaries.mockImplementation(async (ids) => ids.map((id) => summary(id)));

    const sections = await fetchWorkspacePageSections(all);

    expect(pages.getSummaries).toHaveBeenCalledTimes(3);
    expect(pages.getSummaries.mock.calls[0][0]).toHaveLength(WORKSPACE_BATCH_SIZE);
    expect(pages.getSummaries.mock.calls[2][0]).toHaveLength(5);
    expect(sections).toHaveLength(all.length);
    expect(sections[0].pages[0].title).toBe('Page 1');
  });

  test('opts every batch into the shared read scheduler', async () => {
    pages.getSummaries.mockResolvedValue([]);

    await fetchWorkspacePageSections(workspaces(2));

    expect(pages.getSummaries).toHaveBeenCalledWith([1, 2], {
      q: '',
      limit: undefined,
      signal: undefined,
      ...BACKGROUND_READ,
    });
  });

  test('skips a batch that fails without failing the surface', async () => {
    const all = workspaces(WORKSPACE_BATCH_SIZE + 2);
    pages.getSummaries
      .mockRejectedValueOnce(new Error('403'))
      .mockImplementationOnce(async (ids) => ids.map((id) => summary(id)));

    const sections = await fetchWorkspacePageSections(all);

    expect(pages.getSummaries).toHaveBeenCalledTimes(2);
    expect(sections).toHaveLength(2);
    expect(sections[0].workspace.id).toBe(WORKSPACE_BATCH_SIZE + 1);
  });

  test('drops workspaces with no pages from the section list', async () => {
    pages.getSummaries.mockImplementation(async (ids) =>
      ids.filter((id) => id === 2).map((id) => summary(id))
    );

    const sections = await fetchWorkspacePageSections(workspaces(3));

    expect(sections).toHaveLength(1);
    expect(sections[0].workspace.id).toBe(2);
  });

  test('search tags results with the workspace name and caps the total', async () => {
    pages.getSummaries.mockImplementation(async (ids) => ids.map((id) => summary(id)));

    const results = await searchPagesAcrossWorkspaces(
      [
        { id: 1, name: 'Alpha' },
        { id: 2, name: 'Beta' },
      ],
      'bug',
      { cap: 2 }
    );

    expect(pages.getSummaries).toHaveBeenCalledWith([1, 2], {
      q: 'bug',
      limit: 2,
      signal: undefined,
      ...BACKGROUND_READ,
    });
    expect(results).toHaveLength(2);
    expect(results[0]).toMatchObject({ id: 10, workspace_id: 1, workspace_name: 'Alpha' });
    expect(results[1]).toMatchObject({ id: 20, workspace_id: 2, workspace_name: 'Beta' });
  });

  test('search stops batching once the cap is reached', async () => {
    const all = workspaces(WORKSPACE_BATCH_SIZE * 2);
    pages.getSummaries.mockImplementation(async (ids) => ids.map((id) => summary(id)));

    const results = await searchPagesAcrossWorkspaces(all, 'bug', { cap: 2 });

    expect(pages.getSummaries).toHaveBeenCalledTimes(1);
    expect(results).toHaveLength(2);
  });

  test('aborting the signal stops further batches', async () => {
    const all = workspaces(WORKSPACE_BATCH_SIZE * 2);
    const controller = new AbortController();
    pages.getSummaries.mockImplementation(async (ids) => {
      controller.abort();
      return ids.map((id) => summary(id));
    });

    await fetchWorkspacePageSections(all, { signal: controller.signal });

    expect(pages.getSummaries).toHaveBeenCalledTimes(1);
  });
});
