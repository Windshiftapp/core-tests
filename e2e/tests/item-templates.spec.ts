import {
  createTemplateViaAPI,
  createWorkspaceViaAPI,
  listItemTypesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';

/**
 * WI-438 / WI-444: end-to-end coverage for work item templates.
 *
 *  - A SELECTABLE template is offered in the create-modal picker and, on
 *    select, fills the description editor.
 *  - A MANDATORY template, on switching the item type to the one it enforces,
 *    auto-fills the description and locks (disables) the picker.
 *  - The REST create contract is covered by
 *    tests/e2e_security_contracts_test.go; this spec keeps the browser
 *    picker/editor workflows.
 */
test.describe('Work item templates (WI-438)', () => {
  const descriptionEditor = '[role="dialog"] .ProseMirror';

  test('selectable template fills the create-modal description', async ({ page, request }) => {
    const ws = await createWorkspaceViaAPI(request, generateWorkspace());
    await createTemplateViaAPI(request, {
      workspace_id: ws.id,
      name: 'bug-report',
      description_body: 'SELECTABLE_BODY_MARKER — steps to reproduce',
      mode: 'selectable',
    });

    const itemPage = new ItemPage(page);
    await itemPage.gotoWorkspaceBacklog(String(ws.id));
    await page.click('#global-create-button');
    await page.waitForSelector('#work-item-title', { timeout: 5000 });

    // The picker only renders once the per-type template fetch resolves.
    await page.getByTestId('template-picker').click();
    await page.getByRole('option', { name: 'bug-report' }).click();

    await expect(page.locator(descriptionEditor)).toContainText('SELECTABLE_BODY_MARKER');
  });

  test('mandatory template auto-fills and disables the picker on type change', async ({
    page,
    request,
  }) => {
    const ws = await createWorkspaceViaAPI(request, generateWorkspace());

    const itemPage = new ItemPage(page);
    await itemPage.gotoWorkspaceBacklog(String(ws.id));
    await page.click('#global-create-button');
    await page.waitForSelector('#work-item-title', { timeout: 5000 });

    // The item-type picker (distinct from the modal's entity-type selector).
    const typeChip = page.getByTestId('create-item-type-chip');
    const currentName = (await typeChip.innerText()).trim();

    // Open the dropdown (scoped to this chip via aria-controls) and pick a type
    // different from the current one — the template load fires on type change.
    await typeChip.click();
    // Attribute selector (not `#id`) — the generated id may start with a digit,
    // which is an invalid CSS id selector.
    const listbox = page.locator(`[id="${await typeChip.getAttribute('aria-controls')}"]`);
    const optionNames = (await listbox.getByRole('option').allInnerTexts())
      .map((s) => s.trim())
      .filter(Boolean);
    const targetName = optionNames.find((n) => n !== currentName);
    expect(targetName, 'a second item type should be available to switch to').toBeTruthy();

    const types = await listItemTypesViaAPI(request);
    const target = types.find((t) => t.name === targetName);
    expect(target, `item type "${targetName}" should exist`).toBeTruthy();
    await createTemplateViaAPI(request, {
      workspace_id: ws.id,
      name: 'mandatory-template',
      description_body: 'MANDATORY_BODY_MARKER — incident details',
      mode: 'mandatory',
      item_type_ids: [target.id],
    });

    // Selecting the type triggers the template load → mandatory auto-applies.
    await listbox.getByRole('option', { name: targetName, exact: true }).click();

    await expect(page.getByTestId('template-picker-locked')).toBeVisible();
    await expect(page.locator(descriptionEditor)).toContainText('MANDATORY_BODY_MARKER');
  });

  test('admin editor creates a template via the multi-select item-type picker', async ({
    page,
    request,
  }) => {
    const ws = await createWorkspaceViaAPI(request, generateWorkspace());
    const types = await listItemTypesViaAPI(request);
    const target = types.find((t) => t.is_default) ?? types[0];

    await page.goto(`/workspaces/${ws.id}/settings/templates`);
    await page.getByTestId('item-template-add').click();

    await page.locator('#item-template-name').fill('admin-editor-template');

    // Multi-select item-type picker (BasePicker, WI-442): open and pick a type.
    await page.getByTestId('item-template-types').locator('input').click();
    await page.getByTestId(`item-template-type-option-${target.id}`).click();

    // Fill the Markdown body; clicking it also dismisses the picker dropdown.
    const editor = page.locator('[data-testid="item-template-body"] .ProseMirror');
    await editor.click();
    await page.keyboard.type('## Steps to reproduce');

    // A bare Enter inside the editor must NOT submit the modal — the editor
    // and its surrounding modal stay open.
    await page.keyboard.press('Enter');
    await expect(editor).toBeVisible();
    await expect(page.getByTestId('item-template-save')).toBeVisible();

    // Ctrl/Cmd+Enter from inside the editor submits the modal (the shortcut
    // must bubble past ProseMirror's keymap). Save succeeds → workspace_id was
    // sent as an int, not a string.
    await editor.press('ControlOrMeta+Enter');
    await expect(page.getByTestId('item-template-list')).toContainText('admin-editor-template');
    await expect(page.getByTestId('item-template-save')).toBeHidden();
  });
});
