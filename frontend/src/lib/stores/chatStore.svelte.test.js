import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    ai: {
      archiveSession: vi.fn(),
      chat: vi.fn(),
      createStandardSession: vi.fn(),
      getGeneralSession: vi.fn(),
      getSessionMessages: vi.fn(),
      listAvailableStandardAgents: vi.fn(),
      listSessions: vi.fn(),
    },
    llmProviders: {
      getEnabled: vi.fn(),
    },
  },
}));

vi.mock('./agentRuns.svelte.js', () => ({
  agentRuns: { emit: vi.fn() },
}));

import { api } from '../api.js';
import { chatStore } from './chatStore.svelte.js';

describe('durable Standard chat selection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    chatStore.clearHistory();
    api.ai.listSessions.mockResolvedValue([]);
    api.ai.listAvailableStandardAgents.mockResolvedValue([
      { id: 41, name: 'Workspace Guide', handle: 'workspace-guide' },
    ]);
    api.ai.createStandardSession.mockResolvedValue({
      id: 81,
      session_type: 'standard',
      workspace_id: 7,
      agent_profile_id: 41,
      title: 'Workspace Guide',
    });
    api.ai.getSessionMessages.mockImplementation(async (id) => {
      if (id === 81) {
        return [{ id: 91, role: 'assistant', content: 'Stored Standard history' }];
      }
      return [{ id: 12, role: 'assistant', content: 'Stored General history' }];
    });
    api.ai.chat.mockResolvedValue({
      session_id: 81,
      message_id: 93,
      answer: 'Standard answer',
    });
    api.ai.archiveSession.mockResolvedValue(undefined);
    api.ai.getGeneralSession.mockResolvedValue({ id: 11, session_type: 'general' });
  });

  test('creates, resumes, executes, and archives a participant Standard session', async () => {
    await chatStore.prepareWorkspaceOptions(7, true);
    await chatStore.selectConversation('new:41', 7);

    expect(api.ai.createStandardSession).toHaveBeenCalledWith(7, {
      agent_profile_id: 41,
      title: 'Workspace Guide',
    });
    expect(chatStore.sessionType).toBe('standard');
    expect(chatStore.sessionId).toBe(81);
    expect(chatStore.messages).toEqual([
      { id: 91, role: 'assistant', content: 'Stored Standard history' },
    ]);

    chatStore.connectionId = 99;
    await chatStore.sendMessage('Please triage this', { workspace_id: 7 });
    expect(api.ai.chat).toHaveBeenCalledWith('Please triage this', undefined, 81, {
      workspace_id: 7,
    });
    expect(chatStore.messages.at(-1)).toMatchObject({
      role: 'assistant',
      content: 'Standard answer',
    });

    await chatStore.archiveCurrentSession();
    expect(api.ai.archiveSession).toHaveBeenCalledWith(81);
    expect(chatStore.sessionType).toBe('general');
    expect(chatStore.sessionId).toBe(11);
    expect(chatStore.messages).toEqual([
      { id: 12, role: 'assistant', content: 'Stored General history' },
    ]);
  });
});
