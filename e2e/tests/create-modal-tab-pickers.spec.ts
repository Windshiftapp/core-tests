import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * WI-445 / WI-455: keyboard flow on the create-item modal's Type and Workspace
 * chips (both `ChipPicker`s whose dropdown is portalled to <body>, outside the
 * modal's focus trap).
 *
 *  - WI-445: a closed chip must NOT swallow Tab — Tab moves to the next field.
 *  - WI-455: after SELECTING a value the menu closes; focus must return inside
 *    the modal (to the trigger), so the next Tab continues to the next field
 *    instead of falling onto <body>/the logo behind the modal.
 *
 * Tab while the menu is open is left to native handling by design (selection is
 * via Enter/click), so it is not asserted here.
 */
test.describe('Create-modal picker keyboard flow (WI-445 / WI-455)', () => {
  let workspacePage: WorkspacePage;
  let itemPage: ItemPage;

  test.beforeEach(async ({ page }) => {
    workspacePage = new WorkspacePage(page);
    itemPage = new ItemPage(page);

    // A second workspace guarantees the Workspace chip has real options.
    const workspace = generateWorkspace();
    await workspacePage.createWorkspace(workspace);
    const workspaceId = await workspacePage.getWorkspaceId(workspace.name);

    await itemPage.gotoWorkspaceBacklog(workspaceId);
    await page.click('#global-create-button');
    const title = page.locator('#work-item-title');
    await expect(title).toBeVisible({ timeout: 5000 });
    // The modal intentionally assigns initial focus after opening. Treat that
    // as readiness so it cannot interrupt the picker journey that follows.
    await expect(title).toBeFocused({ timeout: 5000 });
  });

  for (const chip of ['create-type-chip', 'create-workspace-chip']) {
    test(`closed ${chip}: Tab moves to the next field (WI-445)`, async ({ page }) => {
      const trigger = page.getByTestId(chip);
      const next =
        chip === 'create-type-chip'
          ? page.getByTestId('create-workspace-chip')
          : page.getByTestId('create-modal-close');
      await trigger.focus();
      await expect(trigger).toBeFocused();

      // The regression: this Tab was swallowed and re-focused the chip.
      await page.keyboard.press('Tab');

      await expect(next).toBeFocused();
    });

    test(`selecting in ${chip}: focus returns to the modal, next Tab stays inside (WI-455)`, async ({
      page,
    }) => {
      const trigger = page.getByTestId(chip);
      const option = page.getByTestId(`${chip}-option`).first();
      const pickerFocusTarget = page.getByTestId(
        chip === 'create-workspace-chip' ? `${chip}-search` : `${chip}-listbox`
      );

      // A user may open the picker with the pointer and continue from the
      // keyboard. Wait for its portalled list to own focus before selecting.
      await trigger.click();

      await expect(option).toBeVisible({ timeout: 5000 });
      await expect(pickerFocusTarget).toBeFocused({ timeout: 5000 });

      // Select the highlighted option. The picker restores focus to its trigger.
      await page.keyboard.press('Enter');
      await expect(option).toBeHidden({ timeout: 5000 });

      // The WI-455 invariant: after selecting, focus is back inside the modal
      // (not on <body>/the logo behind it).
      await expect(trigger).toBeFocused();

      // ...and the next Tab keeps focus inside the modal rather than escaping.
      await page.keyboard.press('Tab');
      const next =
        chip === 'create-type-chip'
          ? page.getByTestId('create-workspace-chip')
          : page.getByTestId('create-modal-close');
      await expect(next).toBeFocused();
    });
  }
});
