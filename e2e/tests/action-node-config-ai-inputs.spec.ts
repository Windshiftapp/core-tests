import {
  createActionViaAPI,
  getActionViaAPI,
  nodeConfigByType,
  openActionEditor,
  saveAction,
  selectNodeByType,
} from '../fixtures/action-editor-helpers';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

function aiAgentAction(stamp: number, overrides: Record<string, unknown> = {}) {
  return {
    name: `ai-agent-${stamp}`,
    trigger_type: 'manual',
    trigger_config: '{}',
    nodes: [
      { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
      {
        id: -2,
        node_type: 'ai_agent',
        node_config: JSON.stringify({
          prompt: 'do something',
          input_fields: [],
          tools: [],
          max_steps: 5,
          output_field: 'agent_answer',
          capability_id: 0,
          ...overrides,
        }),
        position_x: 240,
        position_y: 0,
      },
    ],
    edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
  };
}

// P1-7: ai_agent input_fields is a chip editor; entries persist as an array.
test.describe('Action editor — ai_agent input fields', () => {
  test('adding input fields as chips persists an array', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `ai-inputs-${stamp}`,
      key: `AIN${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'ai agent inputs e2e',
    });

    const action = await createActionViaAPI(request, ws.id, aiAgentAction(stamp));

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'ai_agent');

    const input = page.getByTestId('ai-input-field-add');
    await input.fill('item.title');
    await input.press('Enter');
    await expect(page.getByTestId('ai-input-chip-item.title')).toBeVisible();
    await input.fill('summary');
    await input.press('Enter');
    await expect(page.getByTestId('ai-input-chip-summary')).toBeVisible();

    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    const cfg = nodeConfigByType(fresh, 'ai_agent');
    expect(cfg.input_fields).toEqual(['item.title', 'summary']);
  });

  test('stored input_fields hydrate back to chips', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `ai-inputs-hyd-${stamp}`,
      key: `AIH${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'ai agent inputs hydrate e2e',
    });
    const action = await createActionViaAPI(
      request,
      ws.id,
      aiAgentAction(stamp, { input_fields: ['item.description'] })
    );
    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'ai_agent');
    await expect(page.getByTestId('ai-input-chip-item.description')).toBeVisible();
  });
});
