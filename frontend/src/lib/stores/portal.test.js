import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  auth: {
    internal: false,
    portalCustomer: false,
    portalInternal: false,
  },
  assetReports: {
    getForChannel: vi.fn(),
    getForPortal: vi.fn(),
  },
  assetSets: {
    getAll: vi.fn(),
  },
  portal: {
    getBootstrap: vi.fn(),
  },
  channels: {
    updateConfig: vi.fn(),
  },
  requestTypes: {
    getForChannel: vi.fn(),
    getForPortal: vi.fn(),
  },
}));

vi.mock('../api.js', () => ({
  api: {
    assetReports: mocks.assetReports,
    assetSets: mocks.assetSets,
    portal: mocks.portal,
    channels: mocks.channels,
    requestTypes: mocks.requestTypes,
  },
}));

vi.mock('../router.js', () => ({
  navigate: vi.fn(),
}));

vi.mock('../stores', () => ({
  authStore: {
    get isAuthenticated() {
      return mocks.auth.internal;
    },
  },
}));

vi.mock('./portalAuth.svelte.js', () => ({
  portalAuthStore: {
    get isAuthenticated() {
      return mocks.auth.portalCustomer || mocks.auth.portalInternal;
    },
    get isInternal() {
      return mocks.auth.portalInternal;
    },
  },
}));

vi.mock('./toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

const { portalStore } = await import('./portal.svelte.js');

const publicReports = [{ id: 1, name: 'Audience report', is_active: true }];
const managerReports = [
  ...publicReports,
  {
    id: 2,
    name: 'Inactive definition',
    is_active: false,
    cql_query: 'secret = true',
  },
];
const publicRequestTypes = [{ id: 9, name: 'Audience form', is_active: true }];
const managerRequestTypes = [
  ...publicRequestTypes,
  { id: 10, name: 'Hidden definition', is_active: false },
];

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe('portal asset-report loading', () => {
  beforeEach(() => {
    portalStore.reset();
    vi.clearAllMocks();
    mocks.auth.internal = false;
    mocks.auth.portalCustomer = false;
    mocks.auth.portalInternal = false;
    mocks.portal.getBootstrap.mockResolvedValue({
      portal: {
        channel_id: 7,
        slug: 'support',
        title: 'Support',
        sections: [],
        workspace_ids: [],
      },
      request_types: [],
      asset_reports: publicReports,
    });
    mocks.requestTypes.getForPortal.mockResolvedValue([]);
    mocks.requestTypes.getForChannel.mockResolvedValue(managerRequestTypes);
    mocks.assetReports.getForPortal.mockResolvedValue(publicReports);
    mocks.assetReports.getForChannel.mockResolvedValue(managerReports);
    mocks.assetSets.getAll.mockResolvedValue([{ id: 11 }]);
  });

  it('hydrates only the branded sign-in shell for a guest', async () => {
    mocks.portal.getBootstrap.mockResolvedValueOnce({
      portal: {
        slug: 'support',
        title: 'Support',
      },
      request_types: [],
      asset_reports: [],
    });

    await portalStore.loadPortal('support');

    expect(portalStore.portalData).toEqual({
      slug: 'support',
      title: 'Support',
      workspace_ids: [],
    });
    expect(portalStore.requestTypes).toEqual([]);
    expect(portalStore.assetReports).toEqual([]);
  });

  it.each([
    ['portal customer', { portalCustomer: true }],
    ['internal non-manager', { internal: true }],
    ['channel manager', { internal: true }],
  ])('hydrates the %s audience from the authorized portal bootstrap', async (_viewer, auth) => {
    Object.assign(mocks.auth, auth);

    await portalStore.loadPortal('support');

    expect(mocks.portal.getBootstrap).toHaveBeenCalledWith('support');
    expect(mocks.requestTypes.getForPortal).not.toHaveBeenCalled();
    expect(mocks.assetReports.getForPortal).not.toHaveBeenCalled();
    expect(mocks.assetReports.getForChannel).not.toHaveBeenCalled();
    expect(mocks.assetSets.getAll).not.toHaveBeenCalled();
    expect(portalStore.assetReports).toEqual(publicReports);
  });

  it('uses manager definitions only while customization is open', async () => {
    mocks.auth.internal = true;
    await portalStore.loadPortal('support');

    portalStore.showCustomizePanel = true;
    expect(portalStore.isEditing).toBe(true);
    await vi.waitFor(() => expect(portalStore.assetReports).toEqual(managerReports));
    expect(mocks.assetReports.getForChannel).toHaveBeenCalledWith(7);

    mocks.assetReports.getForPortal.mockClear();
    portalStore.showCustomizePanel = false;
    expect(portalStore.isEditing).toBe(false);
    await vi.waitFor(() => expect(portalStore.assetReports).toEqual(publicReports));
    expect(mocks.assetReports.getForPortal).toHaveBeenCalledWith('support');
  });

  it('uses audience request types normally and manager definitions only while customization is open', async () => {
    mocks.auth.internal = true;
    mocks.portal.getBootstrap.mockResolvedValueOnce({
      portal: {
        channel_id: 7,
        slug: 'support',
        title: 'Support',
        sections: [],
        workspace_ids: [],
      },
      request_types: publicRequestTypes,
      asset_reports: publicReports,
    });
    mocks.requestTypes.getForPortal.mockResolvedValue(publicRequestTypes);
    await portalStore.loadPortal('support');

    expect(portalStore.requestTypes.map((requestType) => requestType.id)).toEqual([9]);

    portalStore.showCustomizePanel = true;
    await vi.waitFor(() =>
      expect(portalStore.requestTypes.map((requestType) => requestType.id)).toEqual([9, 10])
    );
    expect(mocks.requestTypes.getForChannel).toHaveBeenCalledWith(7);

    portalStore.showCustomizePanel = false;
    await vi.waitFor(() =>
      expect(portalStore.requestTypes.map((requestType) => requestType.id)).toEqual([9])
    );
    expect(mocks.requestTypes.getForPortal).toHaveBeenCalledWith('support');
  });

  it('discards delayed manager request types after returning to audience mode', async () => {
    mocks.auth.internal = true;
    mocks.portal.getBootstrap.mockResolvedValueOnce({
      portal: {
        channel_id: 7,
        slug: 'support',
        title: 'Support',
        sections: [],
        workspace_ids: [],
      },
      request_types: publicRequestTypes,
      asset_reports: publicReports,
    });
    const managerLoad = deferred();
    mocks.requestTypes.getForChannel.mockReturnValue(managerLoad.promise);
    mocks.requestTypes.getForPortal.mockResolvedValue(publicRequestTypes);
    await portalStore.loadPortal('support');

    portalStore.showCustomizePanel = true;
    await vi.waitFor(() => expect(mocks.requestTypes.getForChannel).toHaveBeenCalledWith(7));
    portalStore.showCustomizePanel = false;
    await vi.waitFor(() =>
      expect(portalStore.requestTypes.map((requestType) => requestType.id)).toEqual([9])
    );

    managerLoad.resolve(managerRequestTypes);
    await managerLoad.promise;
    await Promise.resolve();

    expect(portalStore.requestTypes.map((requestType) => requestType.id)).toEqual([9]);
  });

  it('does not expose audience data as editable definitions when manager access is denied', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.auth.internal = true;
    mocks.assetReports.getForChannel.mockRejectedValue(
      Object.assign(new Error('Not found'), { status: 404 })
    );
    await portalStore.loadPortal('support');

    portalStore.showCustomizePanel = true;

    await vi.waitFor(() => expect(portalStore.loadingAssetReports).toBe(false));
    expect(mocks.assetReports.getForChannel).toHaveBeenCalledWith(7);
    expect(portalStore.assetReports).toEqual([]);
    errorSpy.mockRestore();
  });

  it('discards a delayed manager response after returning to audience mode', async () => {
    mocks.auth.internal = true;
    await portalStore.loadPortal('support');
    const managerLoad = deferred();
    mocks.assetReports.getForChannel.mockReturnValue(managerLoad.promise);

    portalStore.showCustomizePanel = true;
    await vi.waitFor(() => expect(mocks.assetReports.getForChannel).toHaveBeenCalledWith(7));
    portalStore.showCustomizePanel = false;
    await vi.waitFor(() => expect(portalStore.assetReports).toEqual(publicReports));

    managerLoad.resolve(managerReports);
    await managerLoad.promise;
    await Promise.resolve();

    expect(portalStore.assetReports).toEqual(publicReports);
  });

  it('hydrates authenticated badge data and internal field counts without follow-up GETs', async () => {
    mocks.portal.getBootstrap.mockResolvedValueOnce({
      portal: {
        channel_id: 7,
        slug: 'support',
        title: 'Support',
        sections: [],
        workspace_ids: [],
      },
      request_types: [{ id: 9, field_count: 3 }],
      asset_reports: publicReports,
    });
    await portalStore.loadPortal('support');

    portalStore.hydrateUserBootstrap({
      authenticated: true,
      is_internal: true,
      my_requests: [{ id: 41 }],
      my_approvals: [{ id: 51 }],
    });

    expect(portalStore.requestTypes[0].field_count).toBe(3);
    expect(portalStore.myRequests).toEqual([{ id: 41 }]);
    expect(portalStore.myApprovals).toEqual([{ id: 51 }]);
    expect(mocks.requestTypes.getForPortal).not.toHaveBeenCalled();
    expect(mocks.assetReports.getForPortal).not.toHaveBeenCalled();
  });
});

describe('portal customization save paths', () => {
  beforeEach(() => {
    portalStore.reset();
    vi.clearAllMocks();
    mocks.auth.internal = true;
    mocks.portal.getBootstrap.mockResolvedValue({
      portal: {
        channel_id: 7,
        slug: 'support',
        title: 'Support',
        sections: [],
        workspace_ids: [3],
      },
      request_types: [],
      asset_reports: publicReports,
    });
  });

  it('persists identical configuration through the debounced save and the knowledge-base save', async () => {
    vi.useFakeTimers();
    try {
      await portalStore.loadPortal('support');
      // The store disables the initial-load save guard on a 100ms timer.
      await vi.advanceTimersByTimeAsync(100);

      portalStore.saveCustomizations();
      await vi.advanceTimersByTimeAsync(1000);

      portalStore.saveKnowledgeBaseConfig();
      await vi.runOnlyPendingTimersAsync();

      expect(mocks.channels.updateConfig).toHaveBeenCalledTimes(2);
      const [, debouncedConfig] = mocks.channels.updateConfig.mock.calls[0];
      const [, kbConfig] = mocks.channels.updateConfig.mock.calls[1];
      expect(mocks.channels.updateConfig).toHaveBeenNthCalledWith(1, 7, debouncedConfig);
      expect(kbConfig).toEqual(debouncedConfig);
      expect(debouncedConfig.portal_slug).toBe('support');
      expect(debouncedConfig.portal_workspace_ids).toEqual([3]);
    } finally {
      vi.useRealTimers();
    }
  });
});
