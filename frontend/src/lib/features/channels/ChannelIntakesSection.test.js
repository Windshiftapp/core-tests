import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  delete: vi.fn(),
  errorToast: vi.fn(),
  successToast: vi.fn(),
  getItemTypes: vi.fn(),
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  errorToast: mocks.errorToast,
  successToast: mocks.successToast,
}));

vi.mock('../../api/channels.js', () => ({
  channelIntakes: {
        list: mocks.list,
        create: mocks.create,
        update: mocks.update,
        delete: mocks.delete,
      },
}))

vi.mock('../../api/workspaces.js', () => ({
  workspaces: {
        getItemTypes: mocks.getItemTypes,
      },
}));

import ChannelIntakesSection from './ChannelIntakesSection.svelte';
import { channelIntakes } from '../../api/channels.js';
import { workspaces } from '../../api/workspaces.js';


const PORTALS = [
  {
    id: 9,
    name: 'Partner Portal',
    config: JSON.stringify({ portal_workspace_ids: [2] }),
  },
];
const WORKSPACES = [
  { id: 2, name: 'Support' },
  { id: 3, name: 'Ops' },
];

async function openSelect(name) {
  await fireEvent.click(screen.getByRole('combobox', { name }));
}
async function pickOption(name) {
  await fireEvent.click(await screen.findByRole('option', { name }));
}

describe('ChannelIntakesSection (WI-1644)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.list.mockResolvedValue([]);
    mocks.create.mockResolvedValue({});
    mocks.update.mockResolvedValue({});
    mocks.delete.mockResolvedValue({});
    mocks.getItemTypes.mockResolvedValue([
      { id: 30, name: 'Bug' },
      { id: 31, name: 'Task' },
    ]);
  });

  it('renders each intake with its folder and portal target', async () => {
    mocks.list.mockResolvedValue([
      {
        id: 1,
        folder: 'INBOX',
        portal_channel_id: 9,
        workspace_id: 2,
        item_type_id: 30,
        status: 'enabled',
      },
    ]);

    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    expect(await screen.findByTestId('channel-intake-1')).toBeInTheDocument();
    expect(screen.getByText('INBOX')).toBeInTheDocument();
    expect(screen.getByText(/channel.intakeFeedsPortal/)).toBeInTheDocument();
    expect(screen.getByText(/Partner Portal/)).toBeInTheDocument();
  });

  it('shows a needs_attention intake with its reason', async () => {
    mocks.list.mockResolvedValue([
      {
        id: 2,
        folder: 'INBOX',
        portal_channel_id: 9,
        workspace_id: 2,
        item_type_id: 30,
        status: 'needs_attention',
        status_reason: 'Portal no longer serves workspace 2',
      },
    ]);

    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    expect(await screen.findByText('channel.intakeStatusNeedsAttention')).toBeInTheDocument();
    expect(screen.getByTestId('channel-intake-attention-2')).toHaveTextContent(
      'Portal no longer serves workspace 2'
    );
  });

  it('saves an internal workspace intake', async () => {
    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    await fireEvent.click(await screen.findByTestId('channel-intake-add'));
    await fireEvent.input(screen.getByLabelText('channel.intakeFolder'), {
      target: { value: 'Support' },
    });
    await openSelect('channel.intakeTargetWorkspace');
    await pickOption('Ops');
    await openSelect('channel.itemType');
    await pickOption('Task');
    await fireEvent.click(screen.getByTestId('channel-intake-save'));

    expect(mocks.create).toHaveBeenCalledWith(
      7,
      expect.objectContaining({
        folder: 'Support',
        portal_channel_id: null,
        workspace_id: 3,
        item_type_id: 31,
      })
    );
  });

  it('saves a portal-linked intake scoped to the portal workspace', async () => {
    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    await fireEvent.click(await screen.findByTestId('channel-intake-add'));
    await fireEvent.input(screen.getByLabelText('channel.intakeFolder'), {
      target: { value: 'INBOX' },
    });
    await openSelect('channel.intakeTargetPortal');
    await pickOption('Partner Portal');
    await openSelect('channel.intakeTargetWorkspace');
    await pickOption('Support');
    await openSelect('channel.itemType');
    await pickOption('Bug');
    await fireEvent.click(screen.getByTestId('channel-intake-save'));

    expect(mocks.create).toHaveBeenCalledWith(
      7,
      expect.objectContaining({
        folder: 'INBOX',
        portal_channel_id: 9,
        workspace_id: 2,
        item_type_id: 30,
      })
    );
    await waitFor(() => expect(mocks.successToast).toHaveBeenCalledWith('channel.intakeSaved'));
  });

  it('limits workspace choices to the portal served workspaces', async () => {
    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    await fireEvent.click(await screen.findByTestId('channel-intake-add'));
    await openSelect('channel.intakeTargetPortal');
    await pickOption('Partner Portal');
    await openSelect('channel.intakeTargetWorkspace');

    expect(await screen.findByRole('option', { name: 'Support' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Ops' })).not.toBeInTheDocument();
  });

  it('requires an item type before saving', async () => {
    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    await fireEvent.click(await screen.findByTestId('channel-intake-add'));
    await fireEvent.input(screen.getByLabelText('channel.intakeFolder'), {
      target: { value: 'Support' },
    });
    await openSelect('channel.intakeTargetWorkspace');
    await pickOption('Ops');
    await fireEvent.click(screen.getByTestId('channel-intake-save'));

    expect(mocks.errorToast).toHaveBeenCalledWith('channel.itemTypeRequired');
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it('surfaces the permission denial when the actor cannot manage the target', async () => {
    mocks.create.mockRejectedValue(new Error('permission to manage the intake target is required'));

    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    await fireEvent.click(await screen.findByTestId('channel-intake-add'));
    await fireEvent.input(screen.getByLabelText('channel.intakeFolder'), {
      target: { value: 'Support' },
    });
    await openSelect('channel.intakeTargetWorkspace');
    await pickOption('Ops');
    await openSelect('channel.itemType');
    await pickOption('Task');
    await fireEvent.click(screen.getByTestId('channel-intake-save'));

    expect(mocks.errorToast).toHaveBeenCalledWith(
      'permission to manage the intake target is required'
    );
  });

  it('saves the submission receipt toggle', async () => {
    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    await fireEvent.click(await screen.findByTestId('channel-intake-add'));
    await fireEvent.input(screen.getByLabelText('channel.intakeFolder'), {
      target: { value: 'Support' },
    });
    await openSelect('channel.intakeTargetWorkspace');
    await pickOption('Ops');
    await openSelect('channel.itemType');
    await pickOption('Task');
    const receiptToggle = screen
      .getByTestId('channel-intake-send-confirmation')
      .querySelector('input[type="checkbox"]');
    await fireEvent.click(receiptToggle);
    await fireEvent.click(screen.getByTestId('channel-intake-save'));

    expect(mocks.create).toHaveBeenCalledWith(
      7,
      expect.objectContaining({
        send_confirmation: true,
      })
    );
  });

  it('renders the receipt indicator for an enabled intake', async () => {
    mocks.list.mockResolvedValue([
      {
        id: 3,
        folder: 'INBOX',
        portal_channel_id: null,
        workspace_id: 2,
        item_type_id: 30,
        status: 'enabled',
        send_confirmation: true,
      },
    ]);

    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    expect(await screen.findByTestId('channel-intake-confirmation-3')).toHaveTextContent(
      'channel.intakeConfirmationOn'
    );
  });

  it('loads item types for the selected workspace when editing', async () => {
    mocks.list.mockResolvedValue([
      {
        id: 5,
        folder: 'INBOX',
        portal_channel_id: null,
        workspace_id: 3,
        item_type_id: 31,
        status: 'enabled',
      },
    ]);

    render(ChannelIntakesSection, {
      props: { channelId: 7, workspaces: WORKSPACES, portals: PORTALS },
    });

    await fireEvent.click(await screen.findByTestId('channel-intake-edit-5'));

    expect(mocks.getItemTypes).toHaveBeenCalledWith(3);
    await openSelect('channel.itemType');
    expect(await screen.findByRole('option', { name: 'Bug' })).toBeInTheDocument();
  });
});
