import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * WI-403 regression: the item-detail field pickers (status, priority,
 * assignee, …) render their dropdown through a portal anchored to the
 * trigger. The BasePicker popover migration anchored the portaled menu to a
 * display:none input, which has a zero-size box, so floating-ui parked the
 * dropdown at the viewport origin instead of under the trigger.
 *
 * These assert the dropdown opens adjacent to its trigger — vertically
 * touching it and horizontally aligned — which fails hard if the dropdown
 * snaps back to (0,0) in the top-left corner.
 */
test.describe('Field picker dropdown anchoring (WI-403)', () => {
  let workspacePage: WorkspacePage;
  let itemPage: ItemPage;

  test.beforeEach(async ({ page }) => {
    workspacePage = new WorkspacePage(page);
    itemPage = new ItemPage(page);
    const workspace = generateWorkspace();
    await workspacePage.createWorkspace(workspace);
    const workspaceId = await workspacePage.getWorkspaceId(workspace.name);

    const item = generateItem(0, 'basic');
    await itemPage.createItem(workspaceId, { title: item.title });
    await itemPage.openItemDetailModal(item.title);
  });

  async function expectDropdownAnchoredTo(page, fieldTestId: string) {
    const trigger = page.locator(`[data-testid="${fieldTestId}"]`);
    await trigger.click();

    const dropdown = page.locator('[data-testid="picker-dropdown"]');
    await expect(dropdown).toBeVisible({ timeout: 5000 });

    const triggerBox = await trigger.boundingBox();
    const dropdownBox = await dropdown.boundingBox();
    if (!triggerBox || !dropdownBox) {
      throw new Error('trigger or dropdown has no bounding box');
    }

    // Vertically adjacent: the dropdown's top sits just below the trigger
    // (bottom-start placement, no gutter). The bug parked it at y≈0, far above.
    const verticalGap = dropdownBox.y - (triggerBox.y + triggerBox.height);
    expect(
      Math.abs(verticalGap),
      `dropdown should sit just under the trigger, gap was ${verticalGap}px ` +
        `(trigger.bottom=${triggerBox.y + triggerBox.height}, dropdown.top=${dropdownBox.y})`
    ).toBeLessThan(24);

    // Horizontally aligned with the trigger (lefts align under bottom-start;
    // allow a little floating-ui shift). The bug parked it at x≈0, far left of
    // the right-hand sidebar trigger.
    const horizontalOffset = dropdownBox.x - triggerBox.x;
    expect(
      Math.abs(horizontalOffset),
      `dropdown should align under the trigger, offset was ${horizontalOffset}px ` +
        `(trigger.left=${triggerBox.x}, dropdown.left=${dropdownBox.x})`
    ).toBeLessThan(80);

    // Close the dropdown before the next field.
    await page.keyboard.press('Escape');
    await expect(dropdown).toBeHidden({ timeout: 5000 });
  }

  test('status picker opens under its trigger', async ({ page }) => {
    await expectDropdownAnchoredTo(page, 'status-field');
  });

  test('priority picker opens under its trigger', async ({ page }) => {
    await expectDropdownAnchoredTo(page, 'priority-field');
  });
});
