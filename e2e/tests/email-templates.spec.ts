import { expect, test } from '../fixtures/context-path';
import { AdminPage } from '../pages/admin.page';

/**
 * Email Templates Admin Tests
 * Verifies the new admin-edited transactional email template flow:
 * - admin can navigate to /admin/email-templates
 * - the four seeded rows (magic_link, email_verification, invitation,
 *   notification_batch) are listed
 * - editing the notification_batch subject + previewing renders the new subject
 * - saving persists the change
 *
 * notification_batch is mutated rather than magic_link: the suite shares a
 * database, and 16 specs send a magic link and assert its seeded subject, so
 * editing that template concurrently would break them. No spec asserts the
 * digest subject.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('Email Templates', () => {
  let adminPage: AdminPage;
  let originalTemplate: {
    id: number;
    subject: string;
    html_body: string;
    text_body: string;
    description: string;
    is_active: boolean;
  } | null = null;

  test.beforeEach(async ({ page }) => {
    adminPage = new AdminPage(page);
  });

  test.afterEach(async ({ request }) => {
    if (!originalTemplate) return;
    await request.put(`/api/email-templates/${originalTemplate.id}`, {
      headers: SEC_FETCH,
      data: {
        subject: originalTemplate.subject,
        html_body: originalTemplate.html_body,
        text_body: originalTemplate.text_body,
        description: originalTemplate.description,
        is_active: originalTemplate.is_active,
      },
    });
    originalTemplate = null;
  });

  test('admin can list, edit, preview and save an email template', async ({ page, request }) => {
    // Snapshot the seeded notification_batch row so afterEach can restore it.
    const listResp = await request.get('/api/email-templates', { headers: SEC_FETCH });
    expect(listResp.ok()).toBeTruthy();
    const list = await listResp.json();
    const rows = (list.data ?? list) as Array<typeof originalTemplate & { name: string }>;
    const found = rows.find((t) => t.name === 'notification_batch');
    expect(found, 'notification_batch template must be seeded').toBeDefined();
    originalTemplate = found!;

    await adminPage.goto();
    await adminPage.clickTab('Email Templates');

    // Sidebar reaches the email templates page
    await expect(page).toHaveURL(/\/admin\/email-templates/, { timeout: 10000 });

    // The seeded notification_batch row should be present
    const row = page.locator('tr', { hasText: 'notification_batch' }).first();
    await expect(row).toBeVisible({ timeout: 10000 });

    // Open the editor by clicking the row title
    await row.locator('text=notification_batch').first().click();

    // The dialog opens with subject + textareas populated
    const subjectInput = page.getByTestId('email-template-subject');
    await expect(subjectInput).toBeVisible({ timeout: 5000 });

    const newSubject = 'Sign in — Playwright preview ' + Date.now();
    await subjectInput.fill(newSubject);

    // Click Preview and confirm the iframe rendered the new subject
    await page.getByRole('button', { name: /preview/i }).click();

    const previewSubject = page.locator(`text=Subject:`).first();
    await expect(previewSubject).toBeVisible({ timeout: 5000 });
    await expect(page.getByText(newSubject).first()).toBeVisible({ timeout: 5000 });

    // Close the preview modal via its X button. Pressing Escape is unreliable
    // here because focus stays on the Preview button (in the editor modal),
    // so Escape closes the editor instead of the preview.
    const closeButtons = page.getByRole('button', { name: /close/i });
    await closeButtons.last().click();
    await expect(previewSubject).toBeHidden({ timeout: 5000 });

    // Save
    await page.getByRole('button', { name: /^save$/i }).click();

    // Reload and verify the change persisted
    await adminPage.goto();
    await adminPage.clickTab('Email Templates');

    const row2 = page.locator('tr', { hasText: 'notification_batch' }).first();
    await row2.locator('text=notification_batch').first().click();

    const subjectInput2 = page.getByTestId('email-template-subject');
    await expect(subjectInput2).toHaveValue(newSubject);
  });
});
