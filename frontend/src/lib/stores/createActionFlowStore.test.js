import { describe, expect, it } from 'vitest';
import { createActionFlowStore } from './createActionFlowStore.svelte.js';

function notifyStore() {
  return createActionFlowStore({
    defaultTrigger: 'asset_created',
    nodeConfigDefaults: {
      notify_user: {
        recipient_type: 'specific',
        recipients: [],
        message: 'Asset changed',
      },
    },
  });
}

function actionWithNotifyConfig(config) {
  return {
    id: 7,
    trigger_type: 'asset_created',
    trigger_config: '{}',
    nodes: [
      {
        id: 11,
        node_type: 'trigger',
        node_config: '{}',
        position_x: 0,
        position_y: 0,
      },
      {
        id: 12,
        node_type: 'notify_user',
        node_config: JSON.stringify(config),
        position_x: 200,
        position_y: 0,
      },
    ],
    edges: [],
  };
}

describe('createActionFlowStore notify-user config', () => {
  it('persists one or many recipients using the shared recipients schema', () => {
    const store = notifyStore();
    store.init(
      actionWithNotifyConfig({
        recipient_type: 'specific',
        recipients: ['21'],
        message: 'Review it',
      })
    );

    const notifyNode = store.nodes.find((node) => node.type === 'notify_user');
    store.updateNodeConfig(notifyNode.id, { recipients: ['21', '22'] });

    const saved = store.toApiFormat();
    const config = JSON.parse(
      saved.nodes.find((node) => node.node_type === 'notify_user').node_config
    );
    expect(config).toMatchObject({
      recipient_type: 'specific',
      recipients: ['21', '22'],
      message: 'Review it',
    });
    expect(config).not.toHaveProperty('user_id');
  });

  it('migrates legacy asset user_id config when loading and saving', () => {
    const store = notifyStore();
    store.init(actionWithNotifyConfig({ user_id: 23, message: 'Legacy' }));

    const notifyNode = store.nodes.find((node) => node.type === 'notify_user');
    expect(notifyNode.data.config).toMatchObject({
      recipient_type: 'specific',
      recipients: ['23'],
    });

    const saved = store.toApiFormat();
    const config = JSON.parse(
      saved.nodes.find((node) => node.node_type === 'notify_user').node_config
    );
    expect(config.recipients).toEqual(['23']);
    expect(config).not.toHaveProperty('user_id');
  });

  it('keeps an empty specific-recipient selection empty', () => {
    const store = notifyStore();
    store.init(
      actionWithNotifyConfig({
        recipient_type: 'specific',
        recipients: [],
        message: 'Nobody',
      })
    );

    const saved = store.toApiFormat();
    const config = JSON.parse(
      saved.nodes.find((node) => node.node_type === 'notify_user').node_config
    );
    expect(config.recipient_type).toBe('specific');
    expect(config.recipients).toEqual([]);
  });
});
