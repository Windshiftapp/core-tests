import {
  createActionViaAPI,
  getActionViaAPI,
  nodeConfigByType,
  openActionEditor,
  saveAction,
  selectNodeByType,
} from '../fixtures/action-editor-helpers';
import { createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

// P0-1: notify_user "specific" recipients can be configured by picking users by
// name; selections persist as user-id strings and hydrate back to names.
test.describe('Action editor — notify_user specific recipients', () => {
  function notifyAction(stamp: number, recipients: string[]) {
    return {
      name: `notify-specific-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
        {
          id: -2,
          node_type: 'notify_user',
          node_config: JSON.stringify({
            recipient_type: 'specific',
            recipients,
            message: 'hi',
            include_link: true,
          }),
          position_x: 240,
          position_y: 0,
        },
      ],
      edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
    };
  }

  test('picking two users by name persists recipients as id strings', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `notify-recip-${stamp}`,
      key: `NR${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'notify recipient e2e',
    });
    const u1 = await createUserViaAPI(request, {
      email: `nr1-${stamp}@e.test`,
      username: `nr1${stamp.toString().slice(-7)}`,
      first_name: 'Nora',
      last_name: 'One',
      password_hash: 'password123',
    });
    const u2 = await createUserViaAPI(request, {
      email: `nr2-${stamp}@e.test`,
      username: `nr2${stamp.toString().slice(-7)}`,
      first_name: 'Nils',
      last_name: 'Two',
      password_hash: 'password123',
    });

    const action = await createActionViaAPI(request, ws.id, notifyAction(stamp, []));

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'notify_user');

    const addPicker = page.getByTestId('notify-recipient-add').getByTestId('user-picker-trigger');
    await addPicker.click();
    await page.getByTestId(`user-picker-option-${u1.id}`).click();
    await addPicker.click();
    await page.getByTestId(`user-picker-option-${u2.id}`).click();

    await expect(page.getByTestId(`notify-recipient-chip-${u1.id}`)).toBeVisible();
    await expect(page.getByTestId(`notify-recipient-chip-${u2.id}`)).toBeVisible();

    await saveAction(page, ws.id, action.id);

    const fresh = await getActionViaAPI(request, ws.id, action.id);
    const cfg = nodeConfigByType(fresh, 'notify_user');
    expect(cfg.recipient_type).toBe('specific');
    expect((cfg.recipients as string[]).slice().sort()).toEqual(
      [String(u1.id), String(u2.id)].sort()
    );
  });

  test('stored recipient ids hydrate back to user names', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `notify-hydrate-${stamp}`,
      key: `NH${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'notify hydrate e2e',
    });
    const u1 = await createUserViaAPI(request, {
      email: `nh1-${stamp}@e.test`,
      username: `nh1${stamp.toString().slice(-7)}`,
      first_name: 'Hilde',
      last_name: 'Hydrate',
      password_hash: 'password123',
    });

    const action = await createActionViaAPI(request, ws.id, notifyAction(stamp, [String(u1.id)]));

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'notify_user');

    const chip = page.getByTestId(`notify-recipient-chip-${u1.id}`);
    await expect(chip).toBeVisible();
    await expect(chip).toContainText('Hilde');
    await expect(chip).not.toContainText(`#${u1.id}`);
  });

  test('legacy numeric recipients with no recipient_type hydrate to specific', async ({
    page,
    request,
    allowConsoleError,
  }) => {
    allowConsoleError(/\/api\/logbook\//);
    const stamp = Date.now();
    const ws = await createWorkspaceViaAPI(request, {
      name: `notify-legacy-${stamp}`,
      key: `NL${stamp.toString().slice(-7)}`.toUpperCase(),
      description: 'notify legacy hydrate e2e',
    });
    const u1 = await createUserViaAPI(request, {
      email: `nl1-${stamp}@e.test`,
      username: `nl1${stamp.toString().slice(-7)}`,
      first_name: 'Lena',
      last_name: 'Legacy',
      password_hash: 'password123',
    });

    // Legacy config: just a numeric recipient id, no recipient_type. The editor
    // should infer recipient_type 'specific' and render the chip.
    const action = await createActionViaAPI(request, ws.id, {
      name: `notify-legacy-${stamp}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        { id: -1, node_type: 'trigger', node_config: '{}', position_x: 0, position_y: 0 },
        {
          id: -2,
          node_type: 'notify_user',
          node_config: JSON.stringify({
            recipients: [String(u1.id)],
            message: 'hi',
            include_link: true,
          }),
          position_x: 240,
          position_y: 0,
        },
      ],
      edges: [{ source_node_id: -1, target_node_id: -2, edge_type: 'default' }],
    });

    await openActionEditor(page, ws.id, action.id);
    await selectNodeByType(page, 'notify_user');

    await expect(page.getByTestId(`notify-recipient-chip-${u1.id}`)).toBeVisible();
  });
});
