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

// P1-8: output_field names are validated inline (must be a bare identifier);
// a valid name saves and persists.
test.describe('Action editor — output field validation', () => {
  test('invalid output name shows inline error; valid name saves', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `output-valid-${stamp}`,
      key: `OV${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'output field validation e2e',
    });

    const action = await createActionViaAPI(request, ws.id, {
      name: `output-valid-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
        {
          id: -2,
          node_type: 'ai_agent',
          node_config: JSON.stringify({
            prompt: 'do',
            input_fields: [],
            tools: [],
            max_steps: 5,
            output_field: '',
            capability_id: 0,
          }),
          position_x: 240,
          position_y: 0,
        },
      ],
      edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
    });
    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'ai_agent');

    const output = page.locator('#config-aia-output');
    await output.fill('bad name!');
    await expect(page.getByTestId('output-field-error')).toBeVisible();

    await output.fill('good_name');
    await expect(page.getByTestId('output-field-error')).toHaveCount(0);

    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(nodeConfigByType(fresh, 'ai_agent').output_field).toBe('good_name');
  });

  test('whitespace-padded valid name serializes trimmed', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `output-trim-${stamp}`,
      key: `OT${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'output field trim e2e',
    });

    const action = await createActionViaAPI(request, ws.id, {
      name: `output-trim-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
        {
          id: -2,
          node_type: 'ai_agent',
          node_config: JSON.stringify({
            prompt: 'do',
            input_fields: [],
            tools: [],
            max_steps: 5,
            output_field: '',
            capability_id: 0,
          }),
          position_x: 240,
          position_y: 0,
        },
      ],
      edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
    });
    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'ai_agent');

    // A padded-but-otherwise-valid name validates against the trimmed value, so
    // no inline error — and it must persist trimmed, not with the spaces.
    const output = page.locator('#config-aia-output');
    await output.fill('  padded_name  ');
    await expect(page.getByTestId('output-field-error')).toHaveCount(0);

    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(nodeConfigByType(fresh, 'ai_agent').output_field).toBe('padded_name');
  });
});
