import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api/ai.js', () => ({
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
}));

vi.mock('./agentRuns.svelte.js', () => ({
  agentRuns: { emit: vi.fn() },
}));

import { ai } from '../api/ai.js';
import { chatStore } from './chatStore.svelte.js';
import { llmProviders } from '../api/ai.js';

describe('durable Standard chat selection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    chatStore.clearHistory();
    ai.listSessions.mockResolvedValue([]);
    ai.listAvailableStandardAgents.mockResolvedValue([
      { id: 41, name: 'Workspace Guide', handle: 'workspace-guide' },
    ]);
    ai.createStandardSession.mockResolvedValue({
      id: 81,
      session_type: 'standard',
      workspace_id: 7,
      agent_profile_id: 41,
      title: 'Workspace Guide',
    });
    ai.getSessionMessages.mockImplementation(async (id) => {
      if (id === 81) {
        return [{ id: 91, role: 'assistant', content: 'Stored Standard history' }];
      }
      return [{ id: 12, role: 'assistant', content: 'Stored General history' }];
    });
    ai.chat.mockResolvedValue({
      session_id: 81,
      message_id: 93,
      answer: 'Standard answer',
    });
    ai.archiveSession.mockResolvedValue(undefined);
    ai.getGeneralSession.mockResolvedValue({ id: 11, session_type: 'general' });
  });

  test('creates, resumes, executes, and archives a participant Standard session', async () => {
    await chatStore.prepareWorkspaceOptions(7, true);
    await chatStore.selectConversation('new:41', 7);

    expect(ai.createStandardSession).toHaveBeenCalledWith(7, {
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
    expect(ai.chat).toHaveBeenCalledWith('Please triage this', undefined, 81, {
      workspace_id: 7,
    });
    expect(chatStore.messages.at(-1)).toMatchObject({
      role: 'assistant',
      content: 'Standard answer',
    });

    await chatStore.archiveCurrentSession();
    expect(ai.archiveSession).toHaveBeenCalledWith(81);
    expect(chatStore.sessionType).toBe('general');
    expect(chatStore.sessionId).toBe(11);
    expect(chatStore.messages).toEqual([
      { id: 12, role: 'assistant', content: 'Stored General history' },
    ]);
  });

  test('starts a new General chat without sending prior turns to the server', async () => {
    chatStore.connectionId = 0;
    ai.chat.mockResolvedValue({
      session_id: 11,
      message_id: 14,
      answer: 'Fresh answer',
    });

    await chatStore.startNewGeneralChat();

    expect(chatStore.sessionType).toBe('general');
    expect(chatStore.sessionId).toBe(11);
    expect(chatStore.messages).toEqual([]);

    await chatStore.sendMessage('Fresh question');

    expect(ai.chat).toHaveBeenCalledWith('Fresh question', undefined, 11, undefined, 12);
    expect(chatStore.messages).toEqual([
      { role: 'user', content: 'Fresh question' },
      expect.objectContaining({ role: 'assistant', content: 'Fresh answer' }),
    ]);
  });
});
