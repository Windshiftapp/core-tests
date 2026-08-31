import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    forms: {
      getForms: vi.fn(),
    },
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, fallback) => fallback || key,
}));

vi.mock('../../runtime/contextPath.js', () => ({
  publicBaseURL: () => 'https://windshift.example',
}));

const { api } = await import('../../api.js');
const { default: FormIntegrationPanel } = await import('./FormIntegrationPanel.svelte');

describe('FormIntegrationPanel authenticated embed contract', () => {
  beforeEach(() => vi.clearAllMocks());

  it('offers only the hosted URL when any form requires authentication', async () => {
    api.forms.getForms.mockResolvedValue([
      { id: 7, config: { require_auth: true } },
      { id: 8, config: { require_auth: false } },
    ]);

    render(FormIntegrationPanel, { props: { slug: 'support' } });

    expect(await screen.findByTestId('authenticated-form-embed-warning')).toBeTruthy();
    expect(screen.getByTestId('form-integration-tab-hosted')).toBeTruthy();
    expect(screen.queryByTestId('form-integration-tab-iframe')).toBeNull();
    expect(screen.queryByTestId('form-integration-tab-widget')).toBeNull();
  });

  it('keeps iframe and widget modes for anonymous forms', async () => {
    api.forms.getForms.mockResolvedValue([{ id: 7, config: { require_auth: false } }]);

    render(FormIntegrationPanel, { props: { slug: 'support' } });

    await vi.waitFor(() => expect(api.forms.getForms).toHaveBeenCalledWith('support'));
    expect(screen.queryByTestId('authenticated-form-embed-warning')).toBeNull();
    expect(screen.getByTestId('form-integration-tab-hosted')).toBeTruthy();
    expect(await screen.findByTestId('form-integration-tab-iframe')).toBeTruthy();
    expect(screen.getByTestId('form-integration-tab-widget')).toBeTruthy();
  });

  it('fails closed to the hosted URL when authentication requirements cannot be loaded', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.forms.getForms.mockRejectedValue(new Error('offline'));

    render(FormIntegrationPanel, { props: { slug: 'support' } });

    expect(await screen.findByTestId('form-embed-mode-check-failed')).toBeTruthy();
    expect(screen.getByTestId('form-integration-tab-hosted')).toBeTruthy();
    expect(screen.queryByTestId('form-integration-tab-iframe')).toBeNull();
    expect(screen.queryByTestId('form-integration-tab-widget')).toBeNull();
    errorSpy.mockRestore();
  });
});
