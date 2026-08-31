import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('forms API aggregates', () => {
  let fetchSpy;

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );
    vi.stubGlobal('fetch', fetchSpy);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('uses the public form bootstrap endpoint', async () => {
    const { forms } = await import('./forms.js');

    await forms.getBootstrap('support');

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/forms/support/bootstrap');
  });

  it('uses the complete selected-form detail endpoint', async () => {
    const { forms } = await import('./forms.js');

    await forms.getFormDetail('support', 7);

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/forms/support/forms/7/detail');
  });
});
