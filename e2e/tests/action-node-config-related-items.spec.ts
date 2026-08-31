import {
  chooseSelectOption,
  createActionViaAPI,
  getActionViaAPI,
  nodeConfigByType,
  openActionEditor,
  saveAction,
  selectNodeByType,
} from '../fixtures/action-editor-helpers';
import { createWorkspaceViaAPI, listLinkTypesViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

// P0-3: related_items "linked" relation exposes link type, direction, and a max
// items cap; all persist.
test.describe('Action editor — related_items linked config', () => {
  test('configuring a linked relation persists link type, direction and max items', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `related-linked-${stamp}`,
      key: `RL${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'related items linked e2e',
    });
    const linkTypes = await listLinkTypesViaAPI(request);
    const chosen = linkTypes.find((lt) => lt.name === 'Relates To') ?? linkTypes[0];

    const action = await createActionViaAPI(request, ws.id, {
      name: `related-linked-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
        {
          id: -2,
          node_type: 'related_items',
          node_config: JSON.stringify({ relation: 'descendants', cross_workspace: false }),
          position_x: 240,
          position_y: 0,
        },
      ],
      edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
    });
    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'related_items');

    await chooseSelectOption(page, 'config-related-relation', 'linked');
    await chooseSelectOption(page, 'config-related-link-type', chosen.id);
    await chooseSelectOption(page, 'config-related-direction', 'incoming');
    await page.getByTestId('related-max-items').fill('5');
    // cross_workspace is relation-independent, so the toggle is available for
    // linked relations too — enable it and confirm it serializes.
    await page.getByTestId('related-cross-workspace').click();

    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    const cfg = nodeConfigByType(fresh, 'related_items');
    expect(cfg.relation).toBe('linked');
    expect(cfg.link_type_id).toBe(chosen.id);
    expect(cfg.link_direction).toBe('incoming');
    expect(cfg.max_items).toBe(5);
    expect(cfg.cross_workspace).toBe(true);
  });
});
