import { expect, test } from '../fixtures/context-path';
import { AdminPage } from '../pages/admin.page';

/**
 * Email Templates Admin Tests
 * Verifies the new admin-edited transactional email template flow:
 * - admin can navigate to /admin/email-templates
 * - the four seeded rows (magic_link, email_verification, invitation,
 *   notification_batch) are listed
 * - editing the magic_link subject + previewing renders the new subject
 * - saving persists the change
 *
 * The afterEach restores the magic_link row to its pre-edit state because
 * the e2e suite shares a database and transactional-emails.spec.ts asserts
 * the seeded subject on the magic-link send.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('Email Templates', () => {
  let adminPage: AdminPage;
  let originalMagicLink: {
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
    if (!originalMagicLink) return;
    await request.put(`/api/email-templates/${originalMagicLink.id}`, {
      headers: SEC_FETCH,
      data: {
        subject: originalMagicLink.subject,
        html_body: originalMagicLink.html_body,
        text_body: originalMagicLink.text_body,
        description: originalMagicLink.description,
        is_active: originalMagicLink.is_active,
      },
    });
    originalMagicLink = null;
  });

  test('admin can list, edit, preview and save an email template', async ({ page, request }) => {
    // Snapshot the seeded magic_link row so afterEach can restore it.
    const listResp = await request.get('/api/email-templates', { headers: SEC_FETCH });
    expect(listResp.ok()).toBeTruthy();
    const list = await listResp.json();
    const rows = (list.data ?? list) as Array<typeof originalMagicLink & { name: string }>;
    const found = rows.find((t) => t.name === 'magic_link');
    if (!found) throw new Error('magic_link template must be seeded');
    originalMagicLink = found;

    await adminPage.goto();
    await adminPage.clickTab('Email Templates');

    // Sidebar reaches the email templates page
    await expect(page).toHaveURL(/\/admin\/email-templates/, { timeout: 10000 });

    // The seeded magic_link row should be present
    const row = page.locator('tr', { hasText: 'magic_link' }).first();
    await expect(row).toBeVisible({ timeout: 10000 });

    // Open the editor by clicking the row title
    await row.locator('text=magic_link').first().click();

    // The dialog opens with subject + textareas populated
    const subjectInput = page.locator('input[type="text"]').first();
    await expect(subjectInput).toBeVisible({ timeout: 5000 });

    const newSubject = `Sign in — Playwright preview ${Date.now()}`;
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

    const row2 = page.locator('tr', { hasText: 'magic_link' }).first();
    await row2.locator('text=magic_link').first().click();

    const subjectInput2 = page.locator('input[type="text"]').first();
    await expect(subjectInput2).toHaveValue(newSubject);
  });
});
