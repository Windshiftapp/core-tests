import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
  get: vi.fn(),
  importTemplate: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock('../api/core.js', () => ({ get: mocks.get }));

vi.mock('../api/configuration.js', () => ({
  configurationSets: {
    import: mocks.importTemplate,
    exportUrl: (id) => `/api/configuration-sets/${id}/export`,
    delete: vi.fn(),
  },
}));

vi.mock('../api/notifications.js', () => ({
  notificationSettings: { getAll: vi.fn(() => Promise.resolve([])) },
}));

vi.mock('../api/workflows.js', () => ({
  workflows: { getAll: vi.fn(() => Promise.resolve([])) },
}));

vi.mock('../api/workspaces.js', () => ({
  workspaces: { getPage: vi.fn(() => Promise.resolve({ data: [], pagination: { total: 0 } })) },
}));

vi.mock('../router.js', () => ({ navigate: mocks.navigate }));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) =>
    Object.keys(params).length > 0
      ? `${key}(${Object.values(params).join(',')})`
      : key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: mocks.errorToast,
  successToast: mocks.successToast,
}));

vi.mock('../composables/useConfirm.js', () => ({ confirm: vi.fn(() => Promise.resolve(true)) }));

import ConfigurationSetManager from './ConfigurationSetManager.svelte';

const TEMPLATE = {
  schema_version: 1,
  kind: 'windshift.configuration-set',
  payload: {
    configuration_set: { name: 'Support Desk' },
    workflows: [{ name: 'Support Desk Workflow', transitions: [] }],
  },
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.get.mockImplementation((url) => {
    if (url.startsWith('/configuration-sets')) {
      return Promise.resolve({
        configuration_sets: [],
        pagination: { page: 1, limit: 10, total: 0 },
      });
    }
    return Promise.resolve([]);
  });
  mocks.importTemplate.mockImplementation((template) =>
    Promise.resolve({
      data: { id: 9, name: template.payload.configuration_set.name },
      warnings: [],
    })
  );
});

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

function importFileInput() {
  return document.querySelector('[data-testid="configuration-set-import-file"]');
}

async function pickTemplate(template = TEMPLATE) {
  // jsdom's File does not implement Blob.text(), which the component uses to
  // read the upload; a text() shim exercises the same production path.
  const file = { text: () => Promise.resolve(JSON.stringify(template)) };
  await fireEvent.change(importFileInput(), { target: { files: [file] } });
}

describe('ConfigurationSetManager import-as-clone', () => {
  it('prompts for a name prefilled from the bundle before importing', async () => {
    render(ConfigurationSetManager);
    await waitFor(() => expect(mocks.get).toHaveBeenCalled());

    await pickTemplate();

    const nameInput = await screen.findByTestId('configuration-set-import-name');
    expect(nameInput).toHaveValue('Support Desk Copy');
    // Nothing is written until the operator confirms the name.
    expect(mocks.importTemplate).not.toHaveBeenCalled();
  });

  it('sends the template with the prompted name and preserves the rest', async () => {
    render(ConfigurationSetManager);
    await waitFor(() => expect(mocks.get).toHaveBeenCalled());

    await pickTemplate();
    const nameInput = await screen.findByTestId('configuration-set-import-name');
    await fireEvent.input(nameInput, { target: { value: 'Support Desk EU' } });
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => expect(mocks.importTemplate).toHaveBeenCalledTimes(1));
    expect(mocks.importTemplate).toHaveBeenCalledWith({
      ...TEMPLATE,
      payload: {
        ...TEMPLATE.payload,
        configuration_set: { name: 'Support Desk EU' },
      },
    });
    expect(mocks.successToast).toHaveBeenCalledWith('settings.configSets.importSuccess(Support Desk EU)');
  });

  it('requires a name before importing', async () => {
    render(ConfigurationSetManager);
    await waitFor(() => expect(mocks.get).toHaveBeenCalled());

    await pickTemplate();
    const nameInput = await screen.findByTestId('configuration-set-import-name');
    await fireEvent.input(nameInput, { target: { value: '   ' } });
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    expect(await screen.findByText(/^validation\.requiredField/)).toBeInTheDocument();
    expect(mocks.importTemplate).not.toHaveBeenCalled();
  });

  it('rejects a file that is not a configuration set template', async () => {
    render(ConfigurationSetManager);
    await waitFor(() => expect(mocks.get).toHaveBeenCalled());

    const file = { text: () => Promise.resolve('not json') };
    await fireEvent.change(importFileInput(), { target: { files: [file] } });

    await waitFor(() =>
      expect(mocks.errorToast).toHaveBeenCalledWith('settings.configSets.importInvalidFile')
    );
    expect(screen.queryByTestId('configuration-set-import-name')).toBeNull();
  });
});
