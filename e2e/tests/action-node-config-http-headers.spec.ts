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

// http_request nodes expose a key/value headers editor; rows serialize into the
// `headers` map and hydrate back when the editor is reopened.
test.describe('Action editor — http_request headers', () => {
  function httpAction(stamp: number) {
    return {
      name: `http-headers-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
        {
          id: -2,
          node_type: 'http_request',
          node_config: JSON.stringify({
            method: 'GET',
            url_template: 'https://example.com/api',
            output_field: 'response',
          }),
          position_x: 240,
          position_y: 0,
        },
      ],
      edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
    };
  }

  test('adding header rows persists and hydrates the headers map', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `http-headers-${stamp}`,
      key: `HH${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'http headers e2e',
    });

    const action = await createActionViaAPI(request, ws.id, httpAction(stamp));

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'http_request');

    await page.getByTestId('http-header-add').click();
    await page.getByTestId('http-header-key-0').fill('X-One');
    await page.getByTestId('http-header-value-0').fill('1');
    await page.getByTestId('http-header-add').click();
    await page.getByTestId('http-header-key-1').fill('X-Two');
    await page.getByTestId('http-header-value-1').fill('2');

    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    expect(nodeConfigByType(fresh, 'http_request').headers).toEqual({
      'X-One': '1',
      'X-Two': '2',
    });

    // Reopen the editor and confirm the rows hydrate from the saved map.
    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'http_request');
    await expect(page.getByTestId('http-header-key-0')).toHaveValue('X-One');
    await expect(page.getByTestId('http-header-value-0')).toHaveValue('1');
    await expect(page.getByTestId('http-header-key-1')).toHaveValue('X-Two');
    await expect(page.getByTestId('http-header-value-1')).toHaveValue('2');
  });
});
