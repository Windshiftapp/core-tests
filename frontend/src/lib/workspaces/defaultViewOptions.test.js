import { describe, expect, test } from 'vitest';
import { buildDefaultViewOptions } from './defaultViewOptions.js';

const translate = (key) => key;

describe('buildDefaultViewOptions', () => {
  test('offers the support queue as a default view when it is enabled', () => {
    const options = buildDefaultViewOptions(['board', 'queue'], 'board', translate);

    expect(options).toEqual([
      { value: 'board', label: 'workspaceSettings.views.board' },
      { value: 'queue', label: 'supportQueue.title' },
    ]);
  });

  test('always offers the current value even when it is not in the enabled set', () => {
    const options = buildDefaultViewOptions(['board'], 'overview', translate);

    expect(options).toEqual([
      { value: 'board', label: 'workspaceSettings.views.board' },
      { value: 'overview', label: 'workspaceSettings.views.overview' },
    ]);
  });

  test('drops ids without a known label', () => {
    const options = buildDefaultViewOptions(['board', 'not-a-view'], null, translate);

    expect(options).toEqual([{ value: 'board', label: 'workspaceSettings.views.board' }]);
  });
});
