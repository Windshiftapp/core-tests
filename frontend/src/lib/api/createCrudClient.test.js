import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

const { fetchAPI } = await import('./core.js');
const { createCrudClient } = await import('./createCrudClient.js');

describe('createCrudClient read request options', () => {
  beforeEach(() => {
    fetchAPI.mockReset();
  });

  it('passes request options through plain reads', () => {
    const client = createCrudClient('/things');
    const requestOptions = { signal: new AbortController().signal };

    client.getAll({ state: 'open' }, requestOptions);
    client.get(9, requestOptions);

    expect(fetchAPI).toHaveBeenNthCalledWith(1, '/things?state=open', requestOptions);
    expect(fetchAPI).toHaveBeenNthCalledWith(2, '/things/9', requestOptions);
  });

  it('passes request options through fully parent-scoped reads', () => {
    const client = createCrudClient('/children', { parentPath: '/parents' });
    const requestOptions = { signal: new AbortController().signal };

    client.getAll(3, { state: 'open' }, requestOptions);
    client.get(3, 9, requestOptions);

    expect(fetchAPI).toHaveBeenNthCalledWith(1, '/parents/3/children?state=open', requestOptions);
    expect(fetchAPI).toHaveBeenNthCalledWith(2, '/parents/3/children/9', requestOptions);
  });

  it('passes request options through nested-list and flat-item reads', () => {
    const client = createCrudClient('/children', {
      parentPath: '/parents',
      itemPath: '/children',
    });
    const requestOptions = { signal: new AbortController().signal };

    client.getAll(3, {}, requestOptions);
    client.get(9, requestOptions);

    expect(fetchAPI).toHaveBeenNthCalledWith(1, '/parents/3/children', requestOptions);
    expect(fetchAPI).toHaveBeenNthCalledWith(2, '/children/9', requestOptions);
  });
});
