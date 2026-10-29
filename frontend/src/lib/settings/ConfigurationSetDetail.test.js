import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  get: vi.fn(),
  navigate: vi.fn(),
  update: vi.fn(),
}));

vi.mock('../router.js', async (importOriginal) => ({
  ...(await importOriginal()),
  navigate: mocks.navigate,
}));

vi.mock('../api/approvalSets.js', () => {
  const emptyList = vi.fn(() => Promise.resolve([]));
  return {
  approvalSets: { getAll: emptyList },
};
})

vi.mock('../api/conditionSets.js', () => {
  const emptyList = vi.fn(() => Promise.resolve([]));
  return {
  conditionSets: { getAll: emptyList },
};
})

vi.mock('../api/configuration.js', () => {
  const emptyList = vi.fn(() => Promise.resolve([]));
  return {
  configurationSets: {
        create: mocks.create,
        get: mocks.get,
        update: mocks.update,
      },
  itemTypes: { getAll: emptyList },
  priorities: { getAll: emptyList },
  screens: { getAll: emptyList },
};
})

vi.mock('../api/notifications.js', () => {
  const emptyList = vi.fn(() => Promise.resolve([]));
  return {
  notificationSettings: { getAll: emptyList },
};
})

vi.mock('../api/workflows.js', () => {
  const emptyList = vi.fn(() => Promise.resolve([]));
  return {
  workflows: { getAll: emptyList },
};
})

vi.mock('../api/workspaces.js', () => {
  const emptyList = vi.fn(() => Promise.resolve([]));
  return {
  workspaces: { getAll: emptyList },
};
});

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../stores/toasts.svelte.js', () => ({ errorToast: vi.fn() }));
vi.mock('./LocalizedObjectFields.svelte', () => ({
  default: function LocalizedObjectFields() {
    return { save: vi.fn(), validate: vi.fn() };
  },
}));

import ConfigurationSetDetail from './ConfigurationSetDetail.svelte';
import { currentRoute } from '../router.js';
import { approvalSets } from '../api/approvalSets.js';
import { conditionSets } from '../api/conditionSets.js';
import { configurationSets, itemTypes, priorities, screens } from '../api/configuration.js';
import { notificationSettings } from '../api/notifications.js';
import { workflows } from '../api/workflows.js';
import { workspaces } from '../api/workspaces.js';






const emptyForm = {
  name: '',
  description: '',
  is_default: false,
  differentiate_by_item_type: false,
  workflow_id: null,
  condition_set_id: null,
  approval_set_id: null,
  notification_setting_id: null,
  create_screen_id: null,
  edit_screen_id: null,
  view_screen_id: null,
  default_item_type_id: null,
  workspace_ids: [],
  priority_ids: [],
  item_type_configs: [],
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.create.mockResolvedValue({ id: 43, ...emptyForm, name: 'Release configuration' });
});

afterEach(cleanup);

describe('ConfigurationSetDetail form submission', () => {
  it('creates a configuration set from the shared detail form', async () => {
    currentRoute.set({
      path: '/admin/configuration-sets/new',
      view: 'admin',
      params: { id: 'new' },
      query: {},
    });
    render(ConfigurationSetDetail);

    await fireEvent.input(await screen.findByPlaceholderText('settings.configSets.namePlaceholder'), {
      target: { value: 'Release configuration' },
    });
    await fireEvent.click(screen.getByTestId('config-set-save'));

    await waitFor(() =>
      expect(mocks.create).toHaveBeenCalledWith({
        ...emptyForm,
        name: 'Release configuration',
      })
    );
    expect(mocks.navigate).toHaveBeenCalledWith('/admin/configuration-sets/43');
  });

  it('updates a configuration set from the same detail form', async () => {
    const existing = { id: 42, ...emptyForm, name: 'Existing configuration' };
    mocks.get.mockResolvedValue(existing);
    mocks.update.mockResolvedValue({ ...existing, is_default: true });
    currentRoute.set({
      path: '/admin/configuration-sets/42',
      view: 'admin',
      params: { id: '42' },
      query: {},
    });
    render(ConfigurationSetDetail);

    await waitFor(() => expect(mocks.get).toHaveBeenCalledWith(42));
    await fireEvent.click(screen.getByLabelText('settings.configSets.setAsDefault'));
    await fireEvent.click(screen.getByTestId('config-set-save'));

    await waitFor(() =>
      expect(mocks.update).toHaveBeenCalledWith(42, {
        ...emptyForm,
        name: 'Existing configuration',
        is_default: true,
      })
    );
  });
});
