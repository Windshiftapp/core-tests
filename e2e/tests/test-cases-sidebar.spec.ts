import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const defaultHeaders = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * WI-12: "Test Case Folders look confusing".
 *
 * The old sidebar on /workspaces/:id/tests led with a "No Folder" entry that
 * only counted unassigned cases. Regular folders rendered with an invisible
 * chevron placeholder (~24px), so they looked like children of "No Folder".
 *
 * The fix: replace the entry with "All Tests", which counts every case in
 * the workspace and loads all of them; render it with the same left offset
 * as a root folder so siblings line up.
 */

async function createFolder(
  request: APIRequestContext,
  workspaceId: number,
  name: string,
  parentId: number | null = null
) {
  const res = await request.post(`${BASE_URL}/api/workspaces/${workspaceId}/test-folders`, {
    headers: defaultHeaders,
    data: { name, parent_id: parentId },
  });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

async function createTestCase(
  request: APIRequestContext,
  workspaceId: number,
  title: string,
  folderId: number | null
) {
  const res = await request.post(`${BASE_URL}/api/workspaces/${workspaceId}/test-cases`, {
    headers: defaultHeaders,
    data: {
      title,
      priority: 'medium',
      status: 'active',
      folder_id: folderId,
    },
  });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

function relativeLuminance([red, green, blue]: number[]): number {
  const linear = [red, green, blue].map((channel) => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrastRatio(foreground: string, background: string): number {
  const parse = (color: string) => (color.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
  const foregroundLuminance = relativeLuminance(parse(foreground));
  const backgroundLuminance = relativeLuminance(parse(background));
  return (
    (Math.max(foregroundLuminance, backgroundLuminance) + 0.05) /
    (Math.min(foregroundLuminance, backgroundLuminance) + 0.05)
  );
}

test.describe('Test Cases sidebar — WI-12', () => {
  let workspaceId: number;
  let folderId: number;
  let caseInFolderId: number;
  let caseUnassignedId: number;

  test.beforeEach(async ({ request }) => {
    const ws = generateWorkspace('tc-sidebar');
    const created = await createWorkspaceViaAPI(request, {
      name: ws.name,
      key: ws.key,
      description: ws.description,
    });
    workspaceId = created.id;

    const folder = await createFolder(request, workspaceId, 'Smoke tests');
    folderId = folder.id;

    const inFolder = await createTestCase(request, workspaceId, 'Case in folder', folderId);
    const unassigned = await createTestCase(request, workspaceId, 'Case with no folder', null);
    caseInFolderId = inFolder.id;
    caseUnassignedId = unassigned.id;
  });

  test('root entry is "All Tests" and counts every case in the workspace', async ({ page }) => {
    await page.goto(`/workspaces/${workspaceId}/tests`);

    const allEntry = page.getByTestId('test-folder-all');
    await expect(allEntry).toBeVisible();
    await expect(allEntry).toContainText('All Tests');
    // No stray "No Folder" label anywhere in the sidebar.
    await expect(page.getByText('No Folder', { exact: true })).toHaveCount(0);

    // Count reflects both the in-folder case and the unassigned one.
    await expect(page.getByTestId('test-folder-all-count')).toHaveText('2');

    // The folder is a sibling, not nested under "All Tests".
    const folderEntry = page.getByTestId(`test-folder-${folderId}`);
    await expect(folderEntry).toBeVisible();
    await expect(folderEntry).toContainText('Smoke tests');
    await expect(folderEntry).toContainText('1');
  });

  test('selecting "All Tests" shows cases from every folder', async ({ page }) => {
    await page.goto(`/workspaces/${workspaceId}/tests`);

    // "All Tests" is selected by default (selectedFolder === null on mount).
    await expect(page.locator(`tr[data-test-case-id="${caseInFolderId}"]`)).toBeVisible();
    await expect(page.locator(`tr[data-test-case-id="${caseUnassignedId}"]`)).toBeVisible();

    // Click the folder — the in-folder case stays, the unassigned one goes away.
    await page.getByTestId(`test-folder-${folderId}`).click();
    await expect(page.locator(`tr[data-test-case-id="${caseInFolderId}"]`)).toBeVisible();
    await expect(page.locator(`tr[data-test-case-id="${caseUnassignedId}"]`)).toHaveCount(0);
  });

  test('step shortcut codes are high contrast while shortcut mode is active', async ({ page }) => {
    await page.goto(`/workspaces/${workspaceId}/tests`);

    const shortcut = page.getByTestId(`test-case-steps-shortcut-${caseInFolderId}`);
    await expect(shortcut).toHaveText('S');

    await page.keyboard.press('s');
    await expect(shortcut).toHaveText(/^\d+$/);

    const colors = await shortcut.evaluate((element) => {
      const styles = window.getComputedStyle(element);
      return { foreground: styles.color, background: styles.backgroundColor };
    });
    expect(contrastRatio(colors.foreground, colors.background)).toBeGreaterThanOrEqual(4.5);
  });

  test('chevron collapses and expands a parent folder', async ({ page, request }) => {
    const child = await createFolder(request, workspaceId, 'Child folder', folderId);

    await page.goto(`/workspaces/${workspaceId}/tests`);

    // Parent has a child folder → chevron renders and the child is visible.
    const toggle = page.getByTestId(`test-folder-${folderId}-toggle`);
    await expect(toggle).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-label', 'Collapse folder');
    await expect(page.getByTestId(`test-folder-${child.id}`)).toBeVisible();

    // Collapse — child row disappears and the aria-label flips to expand.
    await toggle.click();
    await expect(page.getByTestId(`test-folder-${child.id}`)).toHaveCount(0);
    await expect(toggle).toHaveAttribute('aria-label', 'Expand folder');

    // Expand again.
    await toggle.click();
    await expect(page.getByTestId(`test-folder-${child.id}`)).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-label', 'Collapse folder');
  });

  test('chevron click does not select the parent folder', async ({ page, request }) => {
    await createFolder(request, workspaceId, 'Child folder', folderId);

    await page.goto(`/workspaces/${workspaceId}/tests`);

    // "All Tests" is the default selection. Clicking the chevron must not
    // re-select the parent folder — stopPropagation on the inner click.
    const caseUnassignedRow = page.locator(`tr[data-test-case-id="${caseUnassignedId}"]`);
    await expect(caseUnassignedRow).toBeVisible();

    await page.getByTestId(`test-folder-${folderId}-toggle`).click();

    // Unassigned case is still shown → selection didn't change to the folder.
    await expect(caseUnassignedRow).toBeVisible();
  });

  test('"All Tests" icon aligns horizontally with root folder icon', async ({ page }) => {
    await page.goto(`/workspaces/${workspaceId}/tests`);

    await expect(page.getByTestId('test-folder-all')).toBeVisible();
    await expect(page.getByTestId(`test-folder-${folderId}`)).toBeVisible();

    const allIcon = await page.getByTestId('test-folder-all-icon').boundingBox();
    const folderIcon = await page.getByTestId(`test-folder-${folderId}-icon`).boundingBox();

    if (!allIcon || !folderIcon) throw new Error('folder icons are not visible');
    // The bug was a ~24px misalignment. Allow 2px for sub-pixel rendering.
    expect(Math.abs(allIcon.x - folderIcon.x)).toBeLessThan(2);
  });
});
