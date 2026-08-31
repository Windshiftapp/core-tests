import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test('create modal sets and submits an optional due date', async ({ page, request }) => {
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace('date-picker'));

  try {
    await page.addInitScript(() => {
      const testWindow = window as typeof window & {
        __createDatePickerOpenCount: number;
      };
      Object.defineProperty(window, '__createDatePickerOpenCount', {
        configurable: true,
        writable: true,
        value: 0,
      });
      HTMLInputElement.prototype.showPicker = function showPicker() {
        testWindow.__createDatePickerOpenCount += 1;
      };
    });
    await page.goto(`/workspaces/${workspace.id}/backlog`);
    await page.waitForLoadState('networkidle');
    await page.locator('#global-create-button').click();

    const dueDateChip = page.getByTestId('create-due-date-chip');
    await expect(dueDateChip).toBeVisible({ timeout: 10_000 });
    await dueDateChip.click();
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (window as typeof window & { __createDatePickerOpenCount: number })
              .__createDatePickerOpenCount
        )
      )
      .toBe(1);

    const dueDate = '2026-09-15';
    const dueDateInput = page.getByTestId('create-due-date-input');
    await expect(dueDateInput).toBeVisible();
    await dueDateInput.click();
    await dueDateInput.fill(dueDate);

    await expect(dueDateInput).toBeHidden();
    await expect(dueDateChip).toHaveAttribute('data-value', dueDate);

    await page.locator('#work-item-title').fill('Due-date picker regression');
    const createResponsePromise = page.waitForResponse(
      (response) => response.request().method() === 'POST' && /\/api\/items$/.test(response.url())
    );
    await page.locator('#create-modal-submit').click();

    const createResponse = await createResponsePromise;
    expect(createResponse.ok()).toBeTruthy();
    expect(createResponse.request().postDataJSON()).toMatchObject({
      due_date: new Date(dueDate).toISOString(),
    });
  } finally {
    await request.delete(`/api/workspaces/${workspace.id}`, {
      headers: SEC_FETCH,
    });
  }
});
