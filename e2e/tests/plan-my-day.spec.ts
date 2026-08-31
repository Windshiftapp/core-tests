import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

test.describe('Plan My Day', () => {
  test('schedules a generated activity and keeps it on the calendar after reload', async ({
    page,
    request,
  }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace('plan-my-day'));
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Planned activity ${Date.now()}`,
    });
    const itemKey = `${workspace.key}-${item.workspace_item_number}`;

    await page.route('**/api/ai/plan-my-day*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          summary: 'A focused plan for today.',
          activities: [
            {
              time: '09:30',
              duration_minutes: 45,
              item_key: itemKey,
              item_id: item.id,
              workspace_id: workspace.id,
              title: item.title,
              reason: 'Highest-priority work.',
            },
          ],
        }),
      });
    });

    await page.goto('/personal/plan');
    await expect(page.getByTestId('plan-my-day-view')).toBeVisible();

    await page.getByTestId('plan-my-day-generate').click();
    const activity = page.getByTestId(`plan-my-day-activity-${item.id}`);
    await expect(activity).toBeVisible();
    await expect(activity).toContainText(item.title);
    await expect(activity).toContainText('09:30');
    await expect(activity).toContainText('45m');

    const scheduleResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(`/api/items/${item.id}/schedule`) &&
        response.ok()
    );
    await page.getByTestId('plan-my-day-add-calendar').click();
    const response = await scheduleResponse;
    expect(response.request().postDataJSON()).toMatchObject({
      workspace_id: workspace.id,
      scheduled_time: '09:30',
      duration_minutes: 45,
    });
    await expect(page.getByTestId('plan-my-day-success')).toBeVisible();

    await page.getByTestId('plan-my-day-view-calendar').click();
    await expect(page).toHaveURL(/\/personal\/calendar$/);
    await expect(page.getByTestId('calendar-view')).toBeVisible();

    const scheduledItem = page.getByTestId(`calendar-scheduled-item-${item.id}`);
    await expect(scheduledItem).toBeVisible();
    await expect(scheduledItem).toContainText(item.title);
    await expect(scheduledItem).toContainText('09:30');

    await page.reload();
    await expect(page.getByTestId('calendar-view')).toBeVisible();
    await expect(scheduledItem).toBeVisible();
    await expect(scheduledItem).toContainText(item.title);
  });
});
