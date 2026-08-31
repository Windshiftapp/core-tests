import { cleanup, render } from '@testing-library/svelte';
import { afterEach, describe, expect, test } from 'vitest';
import { currentRoute } from '../router.js';
import TabNav from './TabNav.svelte';

describe('TabNav grouped tabs', () => {
  afterEach(() => cleanup());

  test('marks a group active when its current child matches', () => {
    currentRoute.set({
      path: '/admin/diagnostics',
      view: 'admin-diagnostics',
      params: {},
      query: { subtab: 'webhooks' },
    });

    const view = render(TabNav, {
      tabs: [
        { id: 'clock', label: 'Overview', matches: ['clock'] },
        { id: 'actions', label: 'Automation', matches: ['actions', 'webhooks'] },
      ],
      basePath: '/admin/diagnostics',
      defaultTab: 'clock',
    });

    expect(view.getByText('Automation')).toHaveAttribute('aria-current', 'page');
    expect(view.getByText('Overview')).not.toHaveAttribute('aria-current');
  });
});
