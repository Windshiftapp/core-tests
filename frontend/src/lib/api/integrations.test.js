import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

const { fetchAPI } = await import('./core.js');
const { itemIntegrationLinks, userIntegrations } = await import('./integrations.js');

describe('integration API read cancellation', () => {
  beforeEach(() => {
    fetchAPI.mockReset();
  });

  it('passes request options through user and item integration reads', () => {
    const requestOptions = { signal: new AbortController().signal };

    userIntegrations.getAvailableProviders(requestOptions);
    userIntegrations.getConnections(requestOptions);
    itemIntegrationLinks.get(42, requestOptions);

    expect(fetchAPI).toHaveBeenNthCalledWith(
      1,
      '/users/me/integration-connections/available',
      requestOptions
    );
    expect(fetchAPI).toHaveBeenNthCalledWith(
      2,
      '/users/me/integration-connections',
      requestOptions
    );
    expect(fetchAPI).toHaveBeenNthCalledWith(3, '/items/42/integration-links', requestOptions);
  });
});
