import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    channels: {
      update: vi.fn(),
      updateConfig: vi.fn(),
      toggle: vi.fn(),
    },
  },
}));

import { api } from '../../api.js';
import {
  parseChannelConfig,
  prepareFormChannelForWorkspace,
  saveChannelSettings,
} from './channelAdmin.js';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('parseChannelConfig', () => {
  test('uses an empty object for absent or blank config', () => {
    expect(parseChannelConfig(null)).toEqual({});
    expect(parseChannelConfig(undefined)).toEqual({});
    expect(parseChannelConfig('')).toEqual({});
    expect(parseChannelConfig('   ')).toEqual({});
  });

  test('accepts object config in either wire representation', () => {
    const config = { portal_slug: 'support' };
    expect(parseChannelConfig(config)).toBe(config);
    expect(parseChannelConfig(JSON.stringify(config))).toEqual(config);
  });

  test.each([false, 0, [], '[1]', 'null', 'false'])('rejects non-object config: %j', (config) => {
    expect(() => parseChannelConfig(config)).toThrow('Channel configuration');
  });

  test('rejects malformed JSON', () => {
    expect(() => parseChannelConfig('{')).toThrow('Channel configuration is invalid JSON');
  });
});

describe('saveChannelSettings', () => {
  test('leaves channel status unchanged when the settings form has no enable control', async () => {
    await saveChannelSettings({
      channel: { id: 7, type: 'form', direction: 'inbound', status: 'enabled' },
      channelFormData: { name: 'Feedback', description: '', category_id: null },
      configRef: null,
    });

    expect(api.channels.update).toHaveBeenCalledOnce();
    expect(api.channels.toggle).not.toHaveBeenCalled();
  });
});

describe('prepareFormChannelForWorkspace', () => {
  test('adds the form workspace and enables the channel before form creation', async () => {
    api.channels.updateConfig.mockResolvedValue({ success: true });
    api.channels.toggle.mockResolvedValue({ status: 'enabled' });

    const result = await prepareFormChannelForWorkspace({
      channel: { id: 7, status: 'disabled' },
      workspaceIds: [],
      workspaceId: 42,
    });

    expect(api.channels.updateConfig).toHaveBeenCalledWith(7, {
      form_workspace_ids: [42],
    });
    expect(api.channels.toggle).toHaveBeenCalledWith(7);
    expect(result).toEqual({ workspaceIds: [42], status: 'enabled' });
  });

  test('preserves an enabled channel that already serves the workspace', async () => {
    const workspaceIds = [42];

    const result = await prepareFormChannelForWorkspace({
      channel: { id: 7, status: 'enabled' },
      workspaceIds,
      workspaceId: 42,
    });

    expect(api.channels.updateConfig).not.toHaveBeenCalled();
    expect(api.channels.toggle).not.toHaveBeenCalled();
    expect(result).toEqual({ workspaceIds, status: 'enabled' });
  });
});
