import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../stores/ui.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  return { uiStore: writable({ reviewFullscreen: false }) };
});

vi.mock('../stores/workspaces.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  return { workspacesStore: writable({ personalWorkspace: null }) };
});

vi.mock('../stores/moduleSettings.js', async () => {
  const { writable } = await import('svelte/store');
  return { moduleSettings: writable({}) };
});

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: { currentUser: null },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../router.js', () => ({
  navigate: vi.fn(),
}));

const { uiStore } = await import('../stores/ui.svelte.js');
const { default: MainRouteContent } = await import('./MainRouteContent.svelte');
const { default: DummyPage } = await import('./__test-fixtures__/DummyPage.svelte');

const route = { params: {}, query: {}, path: '/' };

function lazyComponentsFor({ component = null, loading = false, error = null } = {}) {
  return {
    getComponent: () => component,
    isLoading: () => loading,
    getError: () => error,
    load: vi.fn(),
    retry: vi.fn(),
  };
}

function renderRoute(view, lazyComponents) {
  return render(MainRouteContent, { props: { view, route, lazyComponents } });
}

beforeEach(() => {
  uiStore.set({ reviewFullscreen: false });
});

describe('MainRouteContent app footer', () => {
  it('renders the footer at the end of an opted-in page once it loads', () => {
    renderRoute('homepage', lazyComponentsFor({ component: DummyPage }));

    expect(screen.getByTestId('dummy-page')).toBeInTheDocument();
    expect(screen.getByTestId('app-footer')).toBeInTheDocument();
  });

  it('does not render the footer on a viewport-filling surface', () => {
    renderRoute('workspace-board', lazyComponentsFor({ component: DummyPage }));

    expect(screen.getByTestId('dummy-page')).toBeInTheDocument();
    expect(screen.queryByTestId('app-footer')).not.toBeInTheDocument();
  });

  it('does not render the footer while the route is loading', () => {
    renderRoute('homepage', lazyComponentsFor({ loading: true }));

    expect(screen.queryByTestId('app-footer')).not.toBeInTheDocument();
  });

  it('does not render the footer when the route failed', () => {
    renderRoute('homepage', lazyComponentsFor({ error: new Error('nope') }));

    expect(screen.queryByTestId('app-footer')).not.toBeInTheDocument();
  });

  it('hides the footer in review focus mode', () => {
    uiStore.set({ reviewFullscreen: true });

    renderRoute('homepage', lazyComponentsFor({ component: DummyPage }));

    expect(screen.getByTestId('dummy-page')).toBeInTheDocument();
    expect(screen.queryByTestId('app-footer')).not.toBeInTheDocument();
  });
});
