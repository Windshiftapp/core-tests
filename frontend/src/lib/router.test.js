import { get } from 'svelte/store';
import { beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { currentRoute, initRouter, navigate } from './router.js';

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

describe('channel manager route', () => {
  it('resolves outside the system administration route', () => {
    navigate('/manage/channels');

    expect(get(currentRoute)).toMatchObject({
      path: '/manage/channels',
      view: 'channel-manager',
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
