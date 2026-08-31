import {
  createActionViaAPI,
  getActionViaAPI,
  openActionEditor,
  saveAction,
  selectNodeByType,
} from '../fixtures/action-editor-helpers';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

test('manual action editor saves a workspace role visibility restriction', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `manual-role-selector-${stamp}`,
    key: `MRS${stamp.toString().slice(-7)}`.toUpperCase(),
    description: 'E2E workspace for manual action role restrictions',
  });
  const action = await createActionViaAPI(request, workspace.id, {
    name: `Role-restricted manual action ${stamp}`,
    trigger_type: 'manual',
    nodes: [
      {
        id: -1,
        node_type: 'trigger',
        node_config: '{}',
        position_x: 0,
        position_y: 0,
      },
    ],
  });

  const rolesResponse = await request.get(`${BASE_URL}/api/workspace-roles`, {
    headers: defaultHeaders,
  });
  expect(rolesResponse.ok(), `load workspace roles: ${rolesResponse.status()}`).toBeTruthy();
  const roles = (await rolesResponse.json()) as Array<{ id: number; name: string }>;
  const viewerRole = roles.find((role) => role.name === 'Viewer');
  if (!viewerRole) throw new Error('Viewer workspace role not found');

  await openActionEditor(page, workspace.id, action.id);
  await selectNodeByType(page, 'trigger');

  const accessControl = page.locator('#manual-action-role-selector');
  await expect(accessControl).toBeVisible();
  await page.locator('#manual-action-role-selector-input').click();
  await page.getByTestId(`role-picker-option-${viewerRole.id}`).click();
  await expect(accessControl).toContainText('Viewer');

  await saveAction(page, workspace.id, action.id);
  const saved = await getActionViaAPI(request, workspace.id, action.id);
  expect(saved.allowed_role_ids).toEqual([viewerRole.id]);
});
