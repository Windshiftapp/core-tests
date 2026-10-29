import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  update: vi.fn(),
  updateConfig: vi.fn(),
  toggle: vi.fn(),
  getAll: vi.fn(),
  intakesList: vi.fn(),
  intakesCreate: vi.fn(),
  intakesUpdate: vi.fn(),
  intakesDelete: vi.fn(),
  workspacesLoad: vi.fn(),
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

function readable(value) {
  return {
    subscribe(run) {
      run(value);
      return () => {};
    },
  };
}

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../stores/permissions.svelte.js', () => ({ isSystemAdmin: readable(true) }));
vi.mock('../stores/channelCategories.js', () => ({ channelCategoriesStore: readable([]) }));
vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: mocks.errorToast,
  successToast: mocks.successToast,
}));
vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: { load: mocks.workspacesLoad },
}));
vi.mock('../api/channels.js', () => ({
  channels: {
        update: mocks.update,
        updateConfig: mocks.updateConfig,
        toggle: mocks.toggle,
        getAll: mocks.getAll,
      },
  channelIntakes: {
        list: mocks.intakesList,
        create: mocks.intakesCreate,
        update: mocks.intakesUpdate,
        delete: mocks.intakesDelete,
      },
}));

import ChannelConfigModal from './ChannelConfigModal.svelte';
import { channelIntakes, channels } from '../api/channels.js';

const EMAIL_CHANNEL = {
  id: 5,
  name: 'Windshift Intake',
  type: 'email',
  direction: 'inbound',
  status: 'disabled',
  config: {
    email_auth_method: 'basic',
    imap_host: 'mail.windshift.dev',
    imap_port: 993,
    imap_encryption: 'ssl',
    imap_username: 'intake@windshift.dev',
  },
};

describe('ChannelConfigModal email enable failure', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.update.mockResolvedValue({});
    mocks.updateConfig.mockResolvedValue({});
    mocks.getAll.mockResolvedValue([]);
    mocks.intakesList.mockResolvedValue([]);
    mocks.workspacesLoad.mockResolvedValue([]);
  });

  it('surfaces the server error when enabling an email channel without an intake', async () => {
    const error =
      'Email channel has no intake: add at least one intake (folder and target) before enabling';
    mocks.toggle.mockRejectedValue(new Error(error));

    render(ChannelConfigModal, {
      props: { isOpen: true, channel: EMAIL_CHANNEL, onClose: vi.fn(), onSave: vi.fn() },
    });

    await screen.findByTestId('channel-intake-list');

    const toggle = screen.getByRole('switch');
    await fireEvent.click(toggle);
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    expect(mocks.toggle).toHaveBeenCalledWith(EMAIL_CHANNEL.id);
    await waitFor(() =>
      expect(mocks.errorToast).toHaveBeenCalledWith(`channel.failedToSave: ${error}`)
    );
    expect(mocks.successToast).not.toHaveBeenCalled();
  });
});
