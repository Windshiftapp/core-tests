import { render, screen } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, fallback) => fallback || key,
}));

import ChannelFormConfig from './ChannelFormConfig.svelte';

describe('ChannelFormConfig', () => {
  test('requires only a public URL and keeps activation out of settings', () => {
    const { component } = render(ChannelFormConfig, {
      props: {
        formData: {
          slug: 'feedback',
          workspace_ids: [],
          enabled: false,
          theme: 'light',
          brand_color: '#14b8a6',
          logo_url: '',
          success_message: '',
          redirect_url: '',
        },
      },
    });

    expect(component.validate()).toEqual({ valid: true });
    expect(component.getConfig()).toMatchObject({
      form_slug: 'feedback',
      form_workspace_ids: [],
    });
    expect(screen.queryByText('channel.enableForm')).toBeNull();
    expect(screen.queryByTestId('form-integration-tab-hosted')).toBeNull();
  });
});
