import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/misc.js', () => ({
  jiraImport: {
        analyzeProjects: vi.fn(),
        testConnection: vi.fn(),
        startImport: vi.fn(),
        getJobStatus: vi.fn(),
        getConnections: vi.fn().mockResolvedValue([]),
        updateConnection: vi.fn(),
        getImportJobs: vi.fn().mockResolvedValue([]),
        hideImportJob: vi.fn(),
        deleteImportedData: vi.fn(),
        getProjectCounts: vi.fn().mockResolvedValue({ APP: 2 }),
        getProjects: vi
          .fn()
          .mockResolvedValue([{ id: '1', key: 'APP', name: 'Application', is_team_managed: false }]),
      },
}));

import { jiraImport as jiraImportApi } from '../api/misc.js';
import { jiraImport } from './JiraImportStore.svelte.js';
import { statuses } from '../api/workflows.js';


const baseAnalysis = {
  projects: [{ key: 'APP', name: 'Application', issue_count: 2 }],
  issue_types: [],
  statuses: [],
  custom_fields: [],
  versions: [],
  total_issues: 2,
};

describe('Jira Data Center authentication', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.testConnection.mockResolvedValue({
      connection_id: 'connection-dc',
      instance_info: { display_name: 'Example Jira' },
    });
  });

  it('submits a PAT without a username for Data Center connections', async () => {
    const result = await jiraImport.testConnection(
      'https://jira.example.com',
      'stale-user@example.com',
      'data-center-pat',
      'datacenter'
    );

    expect(result).toEqual({ success: true });
    expect(jiraImportApi.testConnection).toHaveBeenCalledWith({
      instance_url: 'https://jira.example.com',
      email: '',
      api_token: 'data-center-pat',
      deployment_type: 'datacenter',
    });
    expect(jiraImport.connection.email).toBe('');
  });
});

describe('Jira saved connection credentials', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
  });

  it('replaces the stored credentials and reloads the connection list', async () => {
    jiraImportApi.updateConnection.mockResolvedValue({
      id: 'connection-1',
      instance_url: 'https://example.atlassian.net',
      email: 'new@example.com',
      instance_name: 'Renamed Jira',
      deployment_type: 'cloud',
    });
    jiraImportApi.getConnections.mockResolvedValue([
      {
        id: 'connection-1',
        instance_url: 'https://example.atlassian.net',
        email: 'new@example.com',
        instance_name: 'Renamed Jira',
        deployment_type: 'cloud',
      },
    ]);

    const result = await jiraImport.updateSavedConnection('connection-1', {
      email: 'new@example.com',
      api_token: 'rotated-token',
    });

    expect(result.success).toBe(true);
    expect(jiraImportApi.updateConnection).toHaveBeenCalledWith('connection-1', {
      email: 'new@example.com',
      api_token: 'rotated-token',
    });
    expect(jiraImportApi.getConnections).toHaveBeenCalled();
    expect(jiraImport.savedConnections.items).toEqual([
      expect.objectContaining({ id: 'connection-1', instance_name: 'Renamed Jira' }),
    ]);
  });

  it('surfaces the backend error without reloading the list', async () => {
    jiraImportApi.updateConnection.mockRejectedValue(
      new Error('Jira connection test failed')
    );

    const result = await jiraImport.updateSavedConnection('connection-1', {
      email: 'new@example.com',
      api_token: 'bad-token',
    });

    expect(result).toEqual({ success: false, error: 'Jira connection test failed' });
    expect(jiraImportApi.getConnections).not.toHaveBeenCalled();
  });
});

describe('Jira import history', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
  });

  it('clears an import from the history without deleting its data', async () => {
    jiraImportApi.hideImportJob.mockResolvedValue(undefined);
    jiraImportApi.getImportJobs.mockResolvedValue([]);

    const result = await jiraImport.hideImportJob('job-hide');

    expect(result).toEqual({ success: true });
    expect(jiraImportApi.hideImportJob).toHaveBeenCalledWith('job-hide');
    expect(jiraImportApi.getImportJobs).toHaveBeenCalled();
    expect(jiraImportApi.deleteImportedData).not.toHaveBeenCalled();
  });

  it('surfaces the backend error when hiding fails', async () => {
    jiraImportApi.hideImportJob.mockRejectedValue(new Error('Cannot clear an active import'));

    const result = await jiraImport.hideImportJob('job-active');

    expect(result).toEqual({ success: false, error: 'Cannot clear an active import' });
    expect(jiraImportApi.getImportJobs).not.toHaveBeenCalled();
  });
});

async function prepareSelectedProject() {
  jiraImport.useSavedConnection({
    id: 'connection-1',
    instance_url: 'https://example.atlassian.net',
    email: 'admin@example.com',
    deployment_type: 'cloud',
  });
  await jiraImport.loadProjects();
  jiraImport.toggleProject('APP');
}

describe('Jira import Xray wizard step', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2 });
  });

  it('inserts the Xray step only after positive Xray detection', async () => {
    jiraImportApi.analyzeProjects.mockResolvedValue({
      ...baseAnalysis,
      xray: {
        detection_status: 'detected',
        requires_credential: true,
        total_tests: 2,
        test_issue_type_ids: ['10001'],
      },
    });
    await prepareSelectedProject();

    await jiraImport.analyzeProjects();

    expect(jiraImport.wizard.steps.map((step) => step.id)).toEqual([
      'connect',
      'projects',
      'xray',
      'mapping',
      'preview',
      'import',
    ]);
    expect(jiraImport.xray.totalTests).toBe(2);
  });

  it('does not insert the Xray step for same-named non-Xray issue types', async () => {
    jiraImportApi.analyzeProjects.mockResolvedValue({
      ...baseAnalysis,
      xray: {
        detection_status: 'not_detected',
        requires_credential: true,
        total_tests: 0,
      },
    });
    await prepareSelectedProject();

    await jiraImport.analyzeProjects();

    expect(jiraImport.wizard.steps.map((step) => step.id)).toEqual([
      'connect',
      'projects',
      'mapping',
      'preview',
      'import',
    ]);
  });
});

describe('Jira import workspace key collisions', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2 });
  });

  it('uses the suggested alias and blocks the mapping step until it is acknowledged', async () => {
    jiraImportApi.analyzeProjects.mockResolvedValue({
      ...baseAnalysis,
      projects: [
        {
          key: 'APP',
          name: 'Application',
          issue_count: 2,
          workspace_key_collision: true,
          suggested_workspace_key: 'JIRA_APP',
        },
      ],
      xray: {
        detection_status: 'not_detected',
        total_tests: 0,
      },
    });
    await prepareSelectedProject();
    await jiraImport.analyzeProjects();
    jiraImport.goToStepId('mapping');

    expect(jiraImport.mappings.workspaces[0]).toMatchObject({
      jiraKey: 'APP',
      newWorkspaceKey: 'JIRA_APP',
      workspaceKeyCollisionFound: true,
      keyAliasAcknowledged: false,
    });
    expect(jiraImport.canProceed()).toBe(false);

    jiraImport.setWorkspaceKeyAliasAcknowledged('APP', true);

    expect(jiraImport.mappings.workspaces[0].keyAliasAcknowledged).toBe(true);
    expect(jiraImport.canProceed()).toBe(true);
  });
});

describe('Jira import custom-field fidelity', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2 });
  });

  it('retains the backend raw-preservation disposition in the import plan', async () => {
    jiraImportApi.analyzeProjects.mockResolvedValue({
      ...baseAnalysis,
      custom_fields: [
        {
          jira_field_id: 'customfield_10031',
          jira_field_name: 'Persona payload',
          jira_field_type: 'vendor.app:persona',
          windshift_field_type: 'textarea',
          can_map: true,
          preserve_raw: true,
          notes: 'App-owned value will be preserved as JSON text.',
        },
      ],
      xray: {
        detection_status: 'not_detected',
        total_tests: 0,
      },
    });
    await prepareSelectedProject();
    await jiraImport.analyzeProjects();

    expect(jiraImport.mappings.customFields[0]).toMatchObject({
      jiraId: 'customfield_10031',
      windshiftType: 'textarea',
      preserveRaw: true,
      action: 'create',
    });
  });
});

describe('Jira team-managed project selection', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
      { id: '2', key: 'TEAM', name: 'Team project', is_team_managed: true },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2, TEAM: 3 });
  });

  it('includes team-managed projects in select all', async () => {
    jiraImport.useSavedConnection({
      id: 'connection-1',
      instance_url: 'https://example.atlassian.net',
      email: 'admin@example.com',
      deployment_type: 'cloud',
    });
    await jiraImport.loadProjects();

    jiraImport.selectAllProjects();

    expect(jiraImport.projects.selected).toEqual(['APP', 'TEAM']);
  });
});

describe('Jira forced re-import', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2 });
    jiraImportApi.startImport.mockResolvedValue({ job_id: 'job-reimport' });
    jiraImportApi.getJobStatus.mockResolvedValue({
      status: 'completed',
      phase: 'completed',
      progress: {},
    });
  });

  it('sends an explicit force_reimport flag only for the update path', async () => {
    await prepareSelectedProject();

    await jiraImport.startImport(true);

    expect(jiraImportApi.startImport).toHaveBeenCalledOnce();
    expect(jiraImportApi.startImport.mock.calls[0][0]).toMatchObject({
      connection_id: 'connection-1',
      project_keys: ['APP'],
      force_reimport: true,
    });
  });
});

describe('Jira partial import completion', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2 });
    jiraImportApi.startImport.mockResolvedValue({ job_id: 'job-partial' });
    jiraImportApi.getJobStatus.mockResolvedValue({
      status: 'completed_with_errors',
      phase: 'completed_with_errors',
      progress: { imported_issues: 1, failed_issues: 1 },
    });
  });

  it('stops polling and exposes completed-with-errors as a result', async () => {
    await prepareSelectedProject();

    await jiraImport.startImport();

    await vi.waitFor(() => {
      expect(jiraImport.import.result?.status).toBe('completed_with_errors');
    });
    expect(jiraImport.import.error).toBeNull();
    expect(jiraImport.wizard.steps.find((step) => step.id === 'import')?.completed).toBe(true);
  });
});

const interactiveAnalysis = {
  ...baseAnalysis,
  issue_types: [{ id: '10001', name: 'Story', subtask: false, hierarchy_level: 0 }],
  statuses: [
    {
      id: '1',
      name: 'In Progress',
      category_key: 'indeterminate',
      category_name: 'In Progress',
      color: '#0052cc',
    },
  ],
  custom_fields: [
    {
      jira_field_id: 'customfield_1',
      jira_field_name: 'Environment',
      jira_field_type: 'string',
      windshift_field_type: 'text',
      can_map: true,
    },
  ],
  mapping_targets: {
    workspaces: [{ id: 7, key: 'EXST', name: 'Existing target' }],
    item_types: [{ id: 11, name: 'Existing type', hierarchy_level: 0 }],
    statuses: [{ id: 12, name: 'Existing status', category_id: 1, category_name: 'To Do', color: '#ccc' }],
    custom_fields: [{ id: 13, name: 'Existing field', field_type: 'text' }],
  },
  xray: { detection_status: 'not_detected', total_tests: 0 },
};

describe('Jira import polling cancellation', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2 });
    jiraImportApi.startImport.mockResolvedValue({ job_id: 'job-poll' });
  });

  it('stops polling after reset instead of requesting a null job', async () => {
    vi.useFakeTimers();
    try {
      jiraImportApi.getJobStatus.mockResolvedValue({
        status: 'running',
        phase: 'running',
        progress: {},
      });
      await prepareSelectedProject();

      await jiraImport.startImport();
      await vi.advanceTimersByTimeAsync(0);
      expect(jiraImportApi.getJobStatus).toHaveBeenCalledWith('job-poll');

      jiraImport.reset();
      jiraImportApi.getJobStatus.mockClear();

      await vi.advanceTimersByTimeAsync(10_000);
      expect(jiraImportApi.getJobStatus).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('cancels the previous loop when a second import starts', async () => {
    vi.useFakeTimers();
    try {
      jiraImportApi.getJobStatus.mockResolvedValue({
        status: 'running',
        phase: 'running',
        progress: {},
      });
      await prepareSelectedProject();

      await jiraImport.startImport();
      await vi.advanceTimersByTimeAsync(0);

      jiraImportApi.startImport.mockResolvedValue({ job_id: 'job-second' });
      await jiraImport.startImport();
      await vi.advanceTimersByTimeAsync(0);

      const before = jiraImportApi.getJobStatus.mock.calls.length;
      await vi.advanceTimersByTimeAsync(2000);

      expect(jiraImportApi.getJobStatus.mock.calls.length - before).toBe(1);
      expect(jiraImportApi.getJobStatus).toHaveBeenLastCalledWith('job-second');
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('Jira import interactive mapping', () => {
  beforeEach(() => {
    jiraImport.reset();
    vi.clearAllMocks();
    jiraImportApi.getProjects.mockResolvedValue([
      { id: '1', key: 'APP', name: 'Application', is_team_managed: false },
    ]);
    jiraImportApi.getProjectCounts.mockResolvedValue({ APP: 2 });
    jiraImportApi.analyzeProjects.mockResolvedValue(interactiveAnalysis);
    jiraImportApi.startImport.mockResolvedValue({ job_id: 'job-mapping' });
    jiraImportApi.getJobStatus.mockResolvedValue({
      status: 'completed',
      phase: 'completed',
      progress: {},
    });
  });

  it('exposes the backend mapping targets after analysis', async () => {
    await prepareSelectedProject();

    await jiraImport.analyzeProjects();

    expect(jiraImport.mappingTargets).toEqual(interactiveAnalysis.mapping_targets);
  });

  it('emits reuse choices for workspace, issue type, status, and custom field', async () => {
    await prepareSelectedProject();
    await jiraImport.analyzeProjects();

    jiraImport.setWorkspaceMapping('APP', { createNew: false, windshiftId: 7 });
    jiraImport.setIssueTypeMapping('Story', 11, false);
    jiraImport.setStatusMapping('In Progress', 12, false);
    jiraImport.setFieldAction('customfield_1', 'map', 13);

    jiraImport.goToStepId('mapping');
    expect(jiraImport.canProceed()).toBe(true);

    await jiraImport.startImport();

    expect(jiraImportApi.startImport.mock.calls[0][0].mappings).toMatchObject({
      workspaces: [
        { jiraKey: 'APP', createNew: false, windshiftId: 7 },
      ],
      issueTypes: [{ jiraName: 'Story', createNew: false, windshiftId: 11 }],
      statuses: [{ jiraName: 'In Progress', createNew: false, windshiftId: 12 }],
      customFields: [{ jiraId: 'customfield_1', action: 'map', windshiftId: 13 }],
    });
  });

  it('blocks the mapping step when reuse is selected without a target', async () => {
    await prepareSelectedProject();
    await jiraImport.analyzeProjects();

    jiraImport.setWorkspaceMapping('APP', { createNew: false, windshiftId: null });
    jiraImport.goToStepId('mapping');

    expect(jiraImport.canProceed()).toBe(false);
  });

  it('allows reuse of an existing workspace even when the Jira key collides', async () => {
    jiraImportApi.analyzeProjects.mockResolvedValue({
      ...interactiveAnalysis,
      projects: [
        {
          key: 'APP',
          name: 'Application',
          issue_count: 2,
          workspace_key_collision: true,
          suggested_workspace_key: 'JIRA_APP',
        },
      ],
    });
    await prepareSelectedProject();
    await jiraImport.analyzeProjects();

    jiraImport.setWorkspaceMapping('APP', { createNew: false, windshiftId: 7 });
    jiraImport.goToStepId('mapping');

    expect(jiraImport.mappings.workspaces[0].workspaceKeyCollisionFound).toBe(true);
    expect(jiraImport.canProceed()).toBe(true);
  });
});
