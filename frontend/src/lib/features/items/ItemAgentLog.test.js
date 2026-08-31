import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/agentRuns.js', () => ({
  agentRuns: {
    listForItem: vi.fn(),
    listEventsAfter: vi.fn(),
    get: vi.fn(),
    usage: vi.fn(),
    cancel: vi.fn(),
    rerun: vi.fn(),
  },
}));

vi.mock('../../stores', () => ({
  workspacePermissions: {
    canEdit: vi.fn(() => false),
    canAdminWorkspace: vi.fn(() => false),
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en' },
  t: vi.fn((key) => key),
}));

vi.mock('../../composables/useConfirm.js', () => ({
  confirm: vi.fn(),
}));

import { agentRuns } from '../../api/agentRuns.js';
import ItemAgentLog from './ItemAgentLog.svelte';

const run = {
  id: 17,
  status: 'succeeded',
  queued_at: '2026-08-11T20:00:00Z',
};

beforeEach(() => {
  agentRuns.listForItem.mockResolvedValue([run]);
  agentRuns.get.mockResolvedValue(run);
  agentRuns.usage.mockResolvedValue(null);
  agentRuns.listEventsAfter
    .mockResolvedValueOnce([
      {
        id: 1,
        type: 'stdout',
        payload_json: JSON.stringify({
          type: 'tool_start',
          tool: 'bash',
          args: { cmd: 'ws comment add SOFT-33 -m "Done"' },
        }),
      },
      {
        id: 2,
        type: 'stdout',
        payload_json: JSON.stringify({
          type: 'tool_done',
          tool: 'bash',
          output: 'fatal: work item update forbidden\n(exit: exit status 1)',
        }),
      },
      {
        id: 3,
        type: 'stdout',
        payload_json: JSON.stringify({
          type: 'tool_start',
          tool: 'read_file',
          args: { path: 'frontend/src/lib/features/items/ItemAgentLog.svelte' },
        }),
      },
      {
        id: 4,
        type: 'stdout',
        payload_json: JSON.stringify({
          type: 'tool_done',
          tool: 'read_file',
          output: 'successful output stays hidden',
        }),
      },
      {
        id: 5,
        type: 'stdout',
        payload_json: JSON.stringify({
          type: 'comment_failed',
          error: 'server returned 403 Forbidden',
        }),
      },
    ])
    .mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ItemAgentLog', () => {
  it('shows read paths, failed tool output, and automatic comment errors in the transcript', async () => {
    render(ItemAgentLog, { props: { itemId: 33, workspaceId: 4 } });

    const transcript = await screen.findByTestId('agent-log-transcript');
    await waitFor(() => {
      expect(transcript.textContent).toContain('bash failed');
      expect(transcript.textContent).toContain('fatal: work item update forbidden');
      expect(transcript.textContent).toContain(
        '→ read_file frontend/src/lib/features/items/ItemAgentLog.svelte'
      );
      expect(transcript.textContent).toContain('Work-item comment failed');
      expect(transcript.textContent).toContain('server returned 403 Forbidden');
    });
    expect(transcript.textContent).not.toContain('successful output stays hidden');
  });
});
