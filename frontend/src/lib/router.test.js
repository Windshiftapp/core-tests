import { get } from 'svelte/store';
import { beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { currentRoute, initRouter, matchRoute, navigate, setNavigationInterceptor } from './router.js';

beforeAll(() => {
  initRouter();
});

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  document.body.innerHTML = '';
});

function clickLink(href) {
  const anchor = document.createElement('a');
  anchor.href = href;
  document.body.appendChild(anchor);
  const event = new MouseEvent('click', {
    bubbles: true,
    cancelable: true,
    button: 0,
  });
  let wasPrevented = false;
  document.addEventListener(
    'click',
    (clickEvent) => {
      // The router listener was registered first, so this records its decision
      // before suppressing jsdom's unimplemented native document navigation.
      wasPrevented = clickEvent.defaultPrevented;
      clickEvent.preventDefault();
    },
    { once: true }
  );
  anchor.dispatchEvent(event);
  anchor.remove();
  return wasPrevented;
}

function originPrefixedExternalURL() {
  const { protocol, hostname, port, origin } = window.location;
  if (port) return `${protocol}//${hostname}:${port}@evil.test/external`;
  return `${origin}.evil.test/external`;
}

describe('router link interception', () => {
  it('does not intercept an external URL whose text starts with the app origin', () => {
    const externalURL = originPrefixedExternalURL();
    expect(new URL(externalURL).origin).not.toBe(window.location.origin);

    const wasPrevented = clickLink(externalURL);

    expect(wasPrevented).toBe(false);
    expect(window.location.pathname).toBe('/');
  });

  it('leaves same-page fragment links to native browser navigation', () => {
    window.history.replaceState({}, '', '/api-docs');

    const wasPrevented = clickLink('#operation-list');

    expect(wasPrevented).toBe(false);
  });

  it('preserves the fragment when routing to another app page', () => {
    const wasPrevented = clickLink('/api-docs#operation-list');

    expect(wasPrevented).toBe(true);
    expect(window.location.pathname).toBe('/api-docs');
    expect(window.location.hash).toBe('#operation-list');
  });
});

describe('navigation interceptor (mobile editor back guard)', () => {
  it('vetoes a cross-route popstate and restores the current entry', () => {
    navigate('/m/items/5/edit');
    const editorPath = window.location.pathname;

    // The Android back gesture lands on the previous entry before the
    // router's popstate handler runs; the editor's interceptor vetoes it.
    window.history.pushState({}, '', '/m/items/5');
    let checks = 0;
    setNavigationInterceptor(() => {
      checks += 1;
      return true;
    });

    window.dispatchEvent(new PopStateEvent('popstate'));

    expect(checks).toBe(1);
    expect(window.location.pathname).toBe(editorPath);
    expect(get(currentRoute).path).toBe(editorPath);

    setNavigationInterceptor(null);
  });

  it('lets the popstate proceed when the interceptor allows it', () => {
    navigate('/m/items/5/edit');
    window.history.pushState({}, '', '/m/items/5');
    setNavigationInterceptor(() => false);

    window.dispatchEvent(new PopStateEvent('popstate'));

    expect(window.location.pathname).toBe('/m/items/5');
    expect(get(currentRoute).path).toBe('/m/items/5');

    setNavigationInterceptor(null);
  });

  it('ignores same-document pops such as history sentinels', () => {
    navigate('/m/items/5');
    // A sheet's sentinel entry shares the URL — only the state differs.
    window.history.pushState({ mobileSheet: 1 }, '', '/m/items/5');
    setNavigationInterceptor(() => {
      throw new Error('interceptor must not be consulted for same-document pops');
    });

    expect(() => window.dispatchEvent(new PopStateEvent('popstate'))).not.toThrow();
    expect(get(currentRoute).path).toBe('/m/items/5');

    setNavigationInterceptor(null);
  });
});

describe('same-URL navigation recovery', () => {
  it('republishes the route when the URL matches but the route store has drifted', () => {
    navigate('/workspaces/1');
    expect(get(currentRoute).path).toBe('/workspaces/1');

    // A direct history write changes the URL without publishing a route.
    window.history.replaceState({}, '', '/workspaces/1/items/102');

    navigate('/workspaces/1/items/102');

    expect(get(currentRoute)).toMatchObject({
      path: '/workspaces/1/items/102',
      view: 'item-detail',
    });
  });

  it('does not add a history entry when repairing a drifted route', () => {
    navigate('/workspaces/1');
    window.history.replaceState({}, '', '/workspaces/1/items/102');
    const lengthBefore = window.history.length;

    navigate('/workspaces/1/items/102');

    expect(window.history.length).toBe(lengthBefore);
  });
});

describe('delegated admin route', () => {
  it('resolves /manage outside the system administration route', () => {
    navigate('/manage');

    expect(get(currentRoute)).toMatchObject({
      path: '/manage',
      view: 'delegated-admin',
      params: {},
    });
  });

  it('resolves a delegated surface tab', () => {
    navigate('/manage/channels');

    expect(get(currentRoute)).toMatchObject({
      path: '/manage/channels',
      view: 'delegated-admin',
      params: { tab: 'channels' },
    });
  });
});

describe('Agent Studio route', () => {
  it('resolves the workspace agent catalog as a workspace view', () => {
    navigate('/workspaces/7/agents');

    expect(get(currentRoute)).toMatchObject({
      path: '/workspaces/7/agents',
      view: 'workspace-agents',
      params: { id: '7' },
    });
  });

  it('resolves creation before the dynamic profile route', () => {
    navigate('/workspaces/7/agents/new');

    expect(get(currentRoute)).toMatchObject({
      path: '/workspaces/7/agents/new',
      view: 'workspace-agent-create',
      params: { id: '7' },
    });
  });

  it('resolves an agent profile with its stable id', () => {
    navigate('/workspaces/7/agents/42');

    expect(get(currentRoute)).toMatchObject({
      path: '/workspaces/7/agents/42',
      view: 'workspace-agent-profile',
      params: { id: '7', agentId: '42' },
    });
  });

  it('redirects the legacy Coding Agents settings URL to Agent Studio', () => {
    navigate('/workspaces/7/settings/coding-agents');

    expect(window.location.pathname).toBe('/workspaces/7/agents');
    expect(get(currentRoute)).toMatchObject({
      path: '/workspaces/7/agents',
      view: 'workspace-agents',
      params: { id: '7' },
    });
  });
});

describe('Teams routes', () => {
  it('resolves the Teams list route', () => {
    navigate('/teams');

    expect(get(currentRoute)).toMatchObject({
      path: '/teams',
      view: 'teams-list',
    });
  });

  it('resolves a team section route with stable parameters', () => {
    navigate('/teams/42/on-call');

    expect(get(currentRoute)).toMatchObject({
      path: '/teams/42/on-call',
      view: 'team-detail',
      params: { id: '42', section: 'on-call' },
    });
  });
});

describe('public form routes', () => {
  it('resolves a direct form URL with both route parameters', () => {
    navigate('/forms/customer-support/42');

    expect(get(currentRoute)).toMatchObject({
      path: '/forms/customer-support/42',
      view: 'public-form',
      params: {
        slug: 'customer-support',
        formId: '42',
      },
    });
  });

  it('resolves the explicit login route used by form return links', () => {
    navigate('/login?return_to=%2Fforms%2Fcustomer-support%2F42');

    expect(get(currentRoute)).toMatchObject({
      path: '/login',
      view: 'homepage',
    });
  });
});

describe('portal request routes', () => {
  it('resolves a request-type URL with its route parameter and prefill query', () => {
    navigate('/portal/support/request/12?prefill.device=431');

    expect(get(currentRoute)).toMatchObject({
      path: '/portal/support/request/12',
      view: 'portal',
      params: {
        slug: 'support',
        requestTypeId: '12',
      },
      query: {
        'prefill.device': '431',
      },
    });
  });
});

describe('matchRoute', () => {
  it('resolves exact routes without parameters', () => {
    expect(matchRoute('/notifications')).toEqual({ view: 'notifications', params: {} });
  });

  it('captures parameters from parameterized routes', () => {
    expect(matchRoute('/workspaces/42/board')).toEqual({
      view: 'workspace-board',
      params: { id: '42' },
    });
  });

  it('prefers the exact route over a parameterized match', () => {
    expect(matchRoute('/workspaces/7/pages/archived')).toEqual({
      view: 'workspace-pages-archived',
      params: { id: '7' },
    });
  });

  it('returns null for unknown paths so callers can apply their own fallback', () => {
    expect(matchRoute('/definitely-not-a-route')).toBeNull();
  });
});
