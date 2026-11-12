import { expect, test } from '../fixtures/context-path';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * The screens field-layout editor is a shared component (also opened from the
 * workflow designer's transition inspector). This spec guards the admin-page
 * wiring: the row action opens it, it loads the screen's fields, and the header
 * Save persists through the component's exported save().
 */
test.describe('Screens admin field editor', () => {
  test('opens the layout editor from the row actions and saves the fields', async ({
    page,
    request,
  }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const name = `e2e-screen-${stamp}`;

    const createResp = await request.post('/api/v2/screens', {
      headers: SEC_FETCH,
      data: { name, description: 'e2e field editor' },
    });
    expect(
      createResp.ok(),
      `create screen: ${createResp.status()} ${await createResp.text()}`
    ).toBeTruthy();
    const screen = (await createResp.json()).data;

    const fieldsResp = await request.put(`/api/v2/screens/${screen.id}/fields`, {
      headers: SEC_FETCH,
      data: {
        fields: [
          {
            field_type: 'system',
            field_identifier: 'title',
            display_order: 0,
            is_required: true,
            field_width: 'full',
          },
          {
            field_type: 'system',
            field_identifier: 'priority',
            display_order: 1,
            is_required: false,
            field_width: 'half',
          },
        ],
      },
    });
    expect(
      fieldsResp.ok(),
      `seed fields: ${fieldsResp.status()} ${await fieldsResp.text()}`
    ).toBeTruthy();

    await page.goto('/admin/screens');
    await page.getByTestId(`screen-actions-${screen.id}`).click();
    await page.getByTestId('screen-action-fields').click();

    await expect(page.getByTestId('screen-field-layout-editor')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('screen-field-priority')).toBeVisible();

    const savePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/screens/${screen.id}/fields`) && resp.request().method() === 'PUT'
    );
    await page.getByTestId('screens-save-fields').click();
    const saveResp = await savePromise;
    expect(saveResp.ok(), `save fields: ${saveResp.status()}`).toBeTruthy();

    // Saving returns to the screen list.
    await expect(page.getByTestId('screen-field-layout-editor')).toHaveCount(0);
  });
});
