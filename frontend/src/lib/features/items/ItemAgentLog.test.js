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

vi.mock('../../stores/workspacePermissions.svelte.js', () => ({
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
  vi.useRealTimers();
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
        '→ read_file frontend/src/lib/features/items/ItemAgentLog.svelte',
      );
      expect(transcript.textContent).toContain('Work-item comment failed');
      expect(transcript.textContent).toContain('server returned 403 Forbidden');
    });
    expect(transcript.textContent).not.toContain('successful output stays hidden');
  });
});

describe('ItemAgentLog tail recovery (WI-1620)', () => {
  const runningRun = { id: 17, status: 'running', queued_at: '2026-08-11T20:00:00Z' };
  const doneRun = { id: 17, status: 'succeeded', queued_at: '2026-08-11T20:00:00Z' };

  it('retries a transient failure with the event cursor preserved', async () => {
    vi.useFakeTimers();
    agentRuns.listForItem.mockResolvedValue([runningRun]);
    agentRuns.listEventsAfter.mockReset();
    agentRuns.listEventsAfter
      .mockResolvedValueOnce([
        { id: 1, type: 'stdout', payload_json: JSON.stringify({ type: 'message', text: 'first' }) },
      ])
      .mockResolvedValueOnce([
        { id: 2, type: 'stdout', payload_json: JSON.stringify({ type: 'message', text: 'second' }) },
      ])
      .mockResolvedValue([]);
    agentRuns.get.mockReset();
    agentRuns.get
      .mockRejectedValueOnce(Object.assign(new Error('offline'), { code: 'NETWORK_ERROR' }))
      .mockResolvedValue(doneRun);

    render(ItemAgentLog, { props: { itemId: 33, workspaceId: 4 } });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(1500);

    expect(agentRuns.listEventsAfter).toHaveBeenCalledTimes(3);
    // The retry resumes from the last delivered event instead of replaying it.
    expect(agentRuns.listEventsAfter).toHaveBeenNthCalledWith(2, 17, 1, 200);
    const transcript = screen.getByTestId('agent-log-transcript');
    expect(transcript.textContent).toContain('first');
    expect(transcript.textContent).toContain('second');
  });

  it('stops tailing when the run is permanently gone', async () => {
    vi.useFakeTimers();
    agentRuns.listForItem.mockResolvedValue([runningRun]);
    agentRuns.listEventsAfter.mockReset();
    agentRuns.listEventsAfter.mockRejectedValue(Object.assign(new Error('gone'), { status: 404 }));

    render(ItemAgentLog, { props: { itemId: 33, workspaceId: 4 } });
    await vi.advanceTimersByTimeAsync(0);
    expect(agentRuns.listEventsAfter).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(60_000);
    expect(agentRuns.listEventsAfter).toHaveBeenCalledTimes(1);
  });

  it('does not keep tailing after unmount', async () => {
    vi.useFakeTimers();
    agentRuns.listForItem.mockResolvedValue([runningRun]);
    agentRuns.listEventsAfter.mockReset();
    agentRuns.listEventsAfter.mockResolvedValue([]);
    agentRuns.get.mockReset();
    agentRuns.get.mockResolvedValue(runningRun);

    const { unmount } = render(ItemAgentLog, { props: { itemId: 33, workspaceId: 4 } });
    await vi.advanceTimersByTimeAsync(0);
    const callsBefore = agentRuns.listEventsAfter.mock.calls.length;

    unmount();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(agentRuns.listEventsAfter.mock.calls.length).toBe(callsBefore);
  });
});
