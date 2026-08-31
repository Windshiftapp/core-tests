import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  agentBindings: {
    listTemplates: vi.fn(),
    getCandidates: vi.fn(),
    listToolCapabilities: vi.fn(),
    createProfile: vi.fn(),
  },
  api: {
    llmProviders: {
      getEnabled: vi.fn(),
    },
    workspaceSCM: {
      getConnections: vi.fn(),
      getLinkedRepos: vi.fn(),
    },
    actionCapabilities: {
      getForWorkspace: vi.fn(),
    },
    runnerPools: {
      listWorkspaceTokens: vi.fn(),
      mintWorkspaceToken: vi.fn(),
      revokeWorkspaceToken: vi.fn(),
      listWorkspaceInstances: vi.fn(),
    },
    setup: {
      getModuleSettings: vi.fn(),
      updateModuleSettings: vi.fn(),
    },
  },
}));

vi.mock('../../router.js', () => ({
  navigate: vi.fn(),
}));

import { agentBindings, api } from '../../api.js';
import { navigate } from '../../router.js';
import { getShortcutDisplay } from '../../utils/keyboardShortcuts.js';
import AgentCreate from './AgentCreate.svelte';

const storedValues = new Map();
const localStorageMock = {
  clear: () => storedValues.clear(),
  getItem: (key) => storedValues.get(key) ?? null,
  removeItem: (key) => storedValues.delete(key),
  setItem: (key, value) => storedValues.set(key, String(value)),
};

beforeEach(() => {
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: localStorageMock,
  });
  window.localStorage.clear();
  agentBindings.listTemplates.mockResolvedValue([
    {
      key: 'workspace_guide',
      name: 'Workspace Guide',
      default_type: 'standard',
      instructions: 'Answer from workspace context.',
    },
  ]);
  agentBindings.getCandidates.mockResolvedValue([]);
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
  agentBindings.createProfile.mockResolvedValue({ id: 42 });
  api.llmProviders.getEnabled.mockResolvedValue([
    { id: 9, name: 'Primary model', model: 'gpt-example' },
  ]);
  api.workspaceSCM.getConnections.mockResolvedValue([]);
  api.workspaceSCM.getLinkedRepos.mockResolvedValue([]);
  api.actionCapabilities.getForWorkspace.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('AgentCreate', () => {
  it('gives each built-in specialist a distinct purpose-specific icon', async () => {
    agentBindings.listTemplates.mockResolvedValue([
      {
        key: 'workspace_guide',
        name: 'Workspace Guide',
        default_type: 'standard',
      },
      {
        key: 'work_item_triage',
        name: 'Work-item Triage',
        default_type: 'standard',
      },
      {
        key: 'delivery_coordinator',
        name: 'Delivery Coordinator',
        default_type: 'standard',
      },
      {
        key: 'software_engineer',
        name: 'Software Engineer',
        default_type: 'coding',
      },
      { key: 'code_reviewer', name: 'Code Reviewer', default_type: 'coding' },
      {
        key: 'qa_test_engineer',
        name: 'QA / Test Engineer',
        default_type: 'standard',
      },
      {
        key: 'release_manager',
        name: 'Release Manager',
        default_type: 'standard',
      },
      { key: 'blank', name: 'Blank', default_type: 'standard' },
    ]);

    render(AgentCreate, { props: { workspaceId: 7 } });
    await screen.findAllByTestId('agent-template');

    const expectedIcons = {
      workspace_guide: 'map-2',
      work_item_triage: 'list-check',
      delivery_coordinator: 'route',
      software_engineer: 'code',
      code_reviewer: 'git-pull-request',
      qa_test_engineer: 'test-pipe',
      release_manager: 'rocket',
      blank: 'file',
    };

    for (const [templateKey, iconName] of Object.entries(expectedIcons)) {
      const template = document.querySelector(`[data-template-key="${templateKey}"]`);
      expect(template?.querySelector('svg')).toHaveClass(`tabler-icon-${iconName}`);
    }
    expect(new Set(Object.values(expectedIcons)).size).toBe(8);
  });

  it('identifies coding templates as coding agents and explains where they work', async () => {
    agentBindings.listTemplates.mockResolvedValue([
      {
        key: 'software_engineer',
        name: 'Software Engineer',
        default_type: 'coding',
      },
      {
        key: 'workspace_guide',
        name: 'Workspace Guide',
        default_type: 'standard',
      },
    ]);

    render(AgentCreate, { props: { workspaceId: 7 } });
    await screen.findAllByTestId('agent-template');

    const codingTemplate = document.querySelector('[data-template-key="software_engineer"]');
    const standardTemplate = document.querySelector('[data-template-key="workspace_guide"]');
    expect(
      codingTemplate?.querySelector('[data-testid="agent-template-description"]')
    ).toHaveTextContent(
      'Coding agent that works in connected repositories and opens pull requests.'
    );
    expect(
      standardTemplate?.querySelector('[data-testid="agent-template-description"]')
    ).toHaveTextContent('Windshift agent for workspace planning and coordination.');
  });

  it('creates a Draft from the selected immutable template and opens its unified profile', async () => {
    render(AgentCreate, { props: { workspaceId: 7 } });

    const submit = await screen.findByTestId('agent-create-submit');
    await waitFor(() => expect(submit).toBeEnabled());
    expect(submit).toHaveTextContent(getShortcutDisplay('agents', 'create'));
    await fireEvent.input(screen.getByTestId('agent-create-purpose'), {
      target: { value: 'Help members find project context.' },
    });
    await fireEvent.click(submit);

    await waitFor(() =>
      expect(agentBindings.createProfile).toHaveBeenCalledWith(
        7,
        expect.objectContaining({
          template_key: 'workspace_guide',
          profile_type: 'standard',
          name: 'Workspace Guide',
          handle: 'workspace-guide',
          purpose: 'Help members find project context.',
          instructions: 'Answer from workspace context.',
          llm_connection_id: 9,
        })
      )
    );
    expect(navigate).toHaveBeenCalledWith('/workspaces/7/agents/42');
  });

  it('restores an in-progress browser draft and discards it when navigating back', async () => {
    const first = render(AgentCreate, { props: { workspaceId: 7 } });
    const purpose = await screen.findByTestId('agent-create-purpose');
    await fireEvent.input(purpose, {
      target: { value: 'Keep this unfinished profile.' },
    });
    await waitFor(() =>
      expect(window.localStorage.getItem('agent-studio-create:7')).toContain(
        'Keep this unfinished profile.'
      )
    );
    first.unmount();

    render(AgentCreate, { props: { workspaceId: 7 } });
    expect(await screen.findByDisplayValue('Keep this unfinished profile.')).toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('agent-create-back'));
    await waitFor(() => expect(window.localStorage.getItem('agent-studio-create:7')).toBeNull());
    expect(navigate).toHaveBeenCalledWith('/workspaces/7/agents');
  });
});
