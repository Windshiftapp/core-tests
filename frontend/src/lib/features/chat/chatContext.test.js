import { describe, expect, test } from 'vitest';
import { buildChatContext } from './chatContext.js';

describe('buildChatContext', () => {
  test('returns action context with workspace and action ids', () => {
    expect(
      buildChatContext({
        view: 'workspace-actions',
        params: { id: '42', actionId: '7' },
      })
    ).toEqual({ view: 'workspace-actions', workspace_id: 42, action_id: 7 });
  });

  test('returns page context with workspace and current page ids', () => {
    expect(
      buildChatContext({
        view: 'workspace-pages',
        params: { id: '42', pageId: '9' },
      })
    ).toEqual({ view: 'workspace-pages', workspace_id: 42, page_id: 9 });
  });

  test('returns workspace pages context without page id on the pages index', () => {
    expect(
      buildChatContext({
        view: 'workspace-pages',
        params: { id: '42' },
      })
    ).toEqual({ view: 'workspace-pages', workspace_id: 42 });
  });

  test('returns item context with workspace and numeric item ids', () => {
    expect(
      buildChatContext({
        view: 'item-detail',
        params: { id: '42', itemId: '123' },
      })
    ).toEqual({ view: 'item-detail', workspace_id: 42, item_id: 123 });
  });

  test('returns item context without workspace for personal item routes', () => {
    expect(
      buildChatContext({
        view: 'item-detail',
        params: { itemId: '123' },
      })
    ).toEqual({ view: 'item-detail', item_id: 123 });
  });

  test('returns item context with stable item key routes', () => {
    expect(
      buildChatContext({
        view: 'item-detail',
        params: { itemKey: 'WI-348' },
      })
    ).toEqual({ view: 'item-detail', item_key: 'WI-348' });
  });

  test('returns item context from workspace key and number routes', () => {
    expect(
      buildChatContext({
        view: 'item-detail',
        params: { workspaceKey: 'WI', itemNumber: '348' },
      })
    ).toEqual({ view: 'item-detail', item_key: 'WI-348' });
  });

  test('omits context on unrelated routes', () => {
    expect(buildChatContext({ view: 'homepage', params: {} })).toBeUndefined();
  });
});
