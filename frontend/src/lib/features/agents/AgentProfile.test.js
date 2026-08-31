import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      play: () => {},
      pause: () => {},
    });
  }
});

const permissionState = vi.hoisted(() => ({ admin: false }));

vi.mock('../../api.js', () => ({
  agentBindings: {
    listCatalog: vi.fn(),
    listForWorkspace: vi.fn(),
    validateProfile: vi.fn(),
    activateProfile: vi.fn(),
    updateProfile: vi.fn(),
    updateAgentConfig: vi.fn(),
    update: vi.fn(),
    listToolCapabilities: vi.fn(),
    testProfile: vi.fn(),
    migrateLegacyProfile: vi.fn(),
    connectCodingRunner: vi.fn(),
    remove: vi.fn(),
    restore: vi.fn(),
  },
  agentRuns: {
    listForWorkspace: vi.fn(),
    get: vi.fn(),
    cancel: vi.fn(),
  },
  agentSkills: {
    listForWorkspace: vi.fn(),
  },
  api: {
    actionCapabilities: {
      getForWorkspace: vi.fn(),
    },
    runnerPools: {
      listWorkspaceTokens: vi.fn(),
      mintWorkspaceToken: vi.fn(),
      revokeWorkspaceToken: vi.fn(),
      listWorkspaceInstances: vi.fn(),
    },
  },
}));

vi.mock('../../stores', () => ({
  workspacePermissions: {
    canAdminWorkspace: vi.fn(() => permissionState.admin),
  },
}));

import { agentBindings, agentRuns, agentSkills, api } from '../../api.js';
import AgentProfile from './AgentProfile.svelte';

const catalogAgent = {
  id: 42,
  workspace_id: 7,
  name: 'Workspace Guide',
  handle: 'workspace-guide',
  purpose: 'Answers workspace questions.',
  profile_type: 'standard',
  runtime: 'windshift',
  identity_class: 'workspace_managed',
  lifecycle: 'draft',
  availability: 'draft',
  available: false,
  profile_version: 1,
  model_summary: 'gpt-example',
};

beforeEach(() => {
  permissionState.admin = false;
  agentBindings.listCatalog.mockResolvedValue([catalogAgent]);
  agentBindings.listForWorkspace.mockResolvedValue([{ ...catalogAgent, llm_connection_id: 9 }]);
  agentBindings.validateProfile.mockResolvedValue({ ready: true, errors: [] });
  agentBindings.activateProfile.mockResolvedValue({
    ...catalogAgent,
    lifecycle: 'ready',
    profile_version: 2,
  });
  agentBindings.updateProfile.mockResolvedValue({
    ...catalogAgent,
    name: 'Workspace Navigator',
    purpose: 'Guide teammates through delivery.',
    lifecycle: 'draft',
    profile_version: 2,
  });
  agentBindings.updateAgentConfig.mockResolvedValue({ updated: true });
  agentBindings.update.mockResolvedValue({
    ...catalogAgent,
    lifecycle: 'draft',
    profile_version: 2,
    capability_groups: ['issue_management'],
  });
  agentBindings.listToolCapabilities.mockResolvedValue([
    {
      key: 'read_comment',
      label: 'Read and comment',
      required: true,
      tools: [{ name: 'get_item' }],
    },
    {
      key: 'issue_management',
      label: 'Issue management',
      required: false,
      tools: [{ name: 'update_item' }],
    },
  ]);
  agentBindings.testProfile.mockResolvedValue({
    mode: 'standard',
    status: 'succeeded',
    answer: 'I can read this workspace.',
    iterations: 1,
    tool_calls: 0,
  });
  agentBindings.remove.mockResolvedValue(null);
  agentBindings.restore.mockResolvedValue({
    ...catalogAgent,
    lifecycle: 'draft',
  });
  agentRuns.listForWorkspace.mockResolvedValue([
    {
      id: 91,
      workspace_id: 7,
      binding_id: 42,
      item_id: 12,
      job_kind: 'standard_agent',
      profile_version: 1,
      status: 'succeeded',
    },
  ]);
  agentRuns.get.mockResolvedValue({ id: 92, status: 'succeeded', binding_id: 42 });
  agentRuns.cancel.mockResolvedValue({ canceled: true });
  api.actionCapabilities.getForWorkspace.mockResolvedValue([]);
  agentSkills.listForWorkspace.mockResolvedValue([
    {
      id: 5,
      name: 'Product handbook',
      description: 'Approved product knowledge.',
      enabled: true,
    },
  ]);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('AgentProfile', () => {
  it('keeps sensitive readiness administration out of the member profile', async () => {
    render(AgentProfile, { props: { workspaceId: 7, agentId: 42 } });

    expect(await screen.findByText('Workspace Guide')).toBeInTheDocument();
    expect(agentBindings.listCatalog).toHaveBeenCalledWith(7);
    expect(agentBindings.listForWorkspace).not.toHaveBeenCalled();
    expect(screen.queryByTestId('agent-readiness')).not.toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('agent-tab-runs'));
    expect(screen.getByText('Run #91')).toBeInTheDocument();
    expect(window.location.search).toBe('?tab=runs');
  });

  it('opens a directly linked tab and follows browser route updates', async () => {
    permissionState.admin = true;
    const view = render(AgentProfile, {
      props: { workspaceId: 7, agentId: 42, tab: 'knowledge' },
    });

    expect(await screen.findByTestId('agent-knowledge')).toBeInTheDocument();

    await view.rerender({ workspaceId: 7, agentId: 42, tab: 'runs' });
    expect(screen.getByText('Run #91')).toBeInTheDocument();
  });

  it('lets an administrator validate and activate a ready Draft', async () => {
    permissionState.admin = true;

    render(AgentProfile, { props: { workspaceId: 7, agentId: 42 } });

    const activate = await screen.findByTestId('agent-make-ready');
    expect(agentBindings.validateProfile).toHaveBeenCalledWith(7, 42);
    await fireEvent.click(activate);

    await waitFor(() => expect(agentBindings.activateProfile).toHaveBeenCalledWith(7, 42));
    expect(screen.queryByTestId('agent-make-ready')).not.toBeInTheDocument();
  });

  it('archives through the recoverable lifecycle action', async () => {
    permissionState.admin = true;

    render(AgentProfile, { props: { workspaceId: 7, agentId: 42 } });

    const actions = await screen.findByTestId('agent-profile-actions');
    expect(actions).toHaveAttribute('aria-label', 'Agent actions');
    expect(screen.queryByTestId('agent-archive')).not.toBeInTheDocument();

    await fireEvent.click(actions);
    await fireEvent.click(await screen.findByTestId('agent-archive'));
    await fireEvent.click(await screen.findByTestId('agent-archive-dialog-confirm'));

    await waitFor(() => expect(agentBindings.remove).toHaveBeenCalledWith(7, 42));
    expect(screen.getByTestId('agent-restore')).toBeInTheDocument();
  });

  it('saves edited instructions through the existing profile configuration API', async () => {
    permissionState.admin = true;

    render(AgentProfile, {
      props: { workspaceId: 7, agentId: 42, tab: 'instructions' },
    });

    const instructions = await screen.findByTestId('agent-instructions-value');
    await fireEvent.input(instructions, {
      target: { value: 'Updated workspace guidance.' },
    });
    await fireEvent.click(screen.getByTestId('agent-instructions-save'));

    await waitFor(() =>
      expect(agentBindings.updateAgentConfig).toHaveBeenCalledWith(7, 42, {
        instructions: 'Updated workspace guidance.',
        skill_ids: [],
      })
    );
    expect(screen.getByText('Instructions saved as a new Draft version.')).toBeInTheDocument();
  });

  it('attaches reusable knowledge through the existing profile configuration API', async () => {
    permissionState.admin = true;

    render(AgentProfile, {
      props: { workspaceId: 7, agentId: 42, tab: 'knowledge' },
    });

    await fireEvent.click(await screen.findByTestId('agent-knowledge-skill'));
    await fireEvent.click(screen.getByTestId('agent-knowledge-save'));

    await waitFor(() =>
      expect(agentBindings.updateAgentConfig).toHaveBeenCalledWith(7, 42, {
        instructions: '',
        skill_ids: [5],
      })
    );
    expect(screen.getByText('Knowledge sources saved as a new Draft version.')).toBeInTheDocument();
  });

  it('edits Standard capabilities from the canonical tool catalog', async () => {
    permissionState.admin = true;

    render(AgentProfile, {
      props: { workspaceId: 7, agentId: 42, tab: 'tools' },
    });

    expect(await screen.findByTestId('agent-capability-required')).toBeInTheDocument();
    await fireEvent.click(screen.getByTestId('agent-capability-optional'));
    await fireEvent.click(screen.getByTestId('agent-tools-save'));

    await waitFor(() =>
      expect(agentBindings.update).toHaveBeenCalledWith(
        7,
        42,
        expect.objectContaining({
          capability_groups: ['issue_management'],
          llm_connection_id: 9,
        })
      )
    );
    expect(screen.getByText('Tools and access saved as a new Draft version.')).toBeInTheDocument();
  });

  it('edits workspace-managed identity and purpose with optimistic versioning', async () => {
    permissionState.admin = true;

    render(AgentProfile, { props: { workspaceId: 7, agentId: 42 } });

    await fireEvent.input(await screen.findByTestId('agent-overview-name'), {
      target: { value: 'Workspace Navigator' },
    });
    await fireEvent.input(screen.getByTestId('agent-overview-purpose'), {
      target: { value: 'Guide teammates through delivery.' },
    });
    await fireEvent.click(screen.getByTestId('agent-overview-save'));

    await waitFor(() =>
      expect(agentBindings.updateProfile).toHaveBeenCalledWith(
        7,
        42,
        expect.objectContaining({
          expected_version: 1,
          name: 'Workspace Navigator',
          purpose: 'Guide teammates through delivery.',
        })
      )
    );
    expect(screen.getByText('Profile details saved as a new Draft version.')).toBeInTheDocument();
  });

  it('runs a private Standard test without changing readiness', async () => {
    permissionState.admin = true;

    render(AgentProfile, {
      props: { workspaceId: 7, agentId: 42, tab: 'test' },
    });

    const prompt = await screen.findByTestId('agent-test-prompt');
    await fireEvent.input(prompt, {
      target: { value: 'Tell me what you can read.' },
    });
    await fireEvent.click(screen.getByTestId('agent-test-run'));

    await waitFor(() =>
      expect(agentBindings.testProfile).toHaveBeenCalledWith(7, 42, 'Tell me what you can read.')
    );
    expect(screen.getByTestId('agent-test-answer')).toHaveTextContent('I can read this workspace.');
    expect(agentBindings.activateProfile).not.toHaveBeenCalled();
  });
});
