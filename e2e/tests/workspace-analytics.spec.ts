import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

/**
 * Workspace analytics dashboard.
 *
 * Guards the design-system refactor: the page must render its metrics through
 * the shared StatCard tiles and its tables through the shared DataTable rather
 * than the hand-rolled markup it used to carry.
 */
test.describe('Workspace analytics', () => {
  let workspaceId: number;

  // beforeAll runs once per worker, so the key has to carry the worker index —
  // two workers starting in the same millisecond would otherwise collide on it.
  test.beforeAll(async ({ request }, testInfo) => {
    const suffix = `${testInfo.workerIndex}${Date.now().toString().slice(-6)}`;
    const workspace = await createWorkspaceViaAPI(request, {
      name: `Analytics ${suffix}`,
      key: `AN${suffix}`,
      description: 'Analytics dashboard e2e workspace',
    });
    workspaceId = workspace.id;

    for (let i = 1; i <= 3; i++) {
      await createItemViaAPI(request, workspaceId, {
        title: `Analytics sample item ${i}`,
        description: 'Seeded for the analytics dashboard e2e.',
      });
    }
  });

  test('renders health, throughput, aging and delivery panels', async ({ page }) => {
    await page.goto(`/workspaces/${workspaceId}/analytics`);

    const shell = page.getByTestId('analytics-page');
    await expect(shell).toBeVisible();
    await expect(page.getByTestId('analytics-loading')).toBeHidden();
    await expect(page.getByTestId('analytics-error')).toBeHidden();

    // Filters render through the shared Select/Input components.
    await expect(page.getByTestId('analytics-filters')).toBeVisible();
    await expect(page.locator('#analytics-collection')).toBeVisible();
    await expect(page.locator('#analytics-range')).toBeVisible();
    await expect(page.locator('#analytics-start-date')).toBeVisible();
    await expect(page.locator('#analytics-end-date')).toBeVisible();

    // Health metrics render as six shared StatCard tiles.
    const healthStats = page.getByTestId('analytics-health-stats');
    await expect(healthStats).toBeVisible();
    await expect(healthStats.locator('dl')).toHaveCount(6);

    // Attention items render through the shared DataTable.
    const attention = page.getByTestId('analytics-attention-table');
    await expect(attention).toBeVisible();
    await expect(attention.locator('table thead th').first()).toBeVisible();
    await expect(attention.locator('table tbody tr')).toHaveCount(3);

    // Throughput + delivery stat strips are StatCard grids too.
    await expect(page.getByTestId('analytics-throughput-stats').locator('dl')).toHaveCount(3);

    // No hand-rolled analytics table markup survives the refactor.
    await expect(shell.locator('table.analytics-table')).toHaveCount(0);
  });

  /**
   * The flow panels sit in a row subgrid so their cards start at the same y
   * even when one section subtitle wraps to two lines and the other does not.
   *
   * Whether the longer subtitle wraps depends on the viewer's font size and
   * zoom, not just the viewport, so the wrap is forced here by enlarging the
   * subtitle rather than by guessing a width at which it happens naturally.
   */
  test('flow panel cards align despite unequal header heights', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1400 });
    await page.goto(`/workspaces/${workspaceId}/analytics`);

    const throughput = page.getByTestId('analytics-throughput-panel');
    const aging = page.getByTestId('analytics-aging-panel');
    await expect(throughput).toBeVisible();
    await expect(aging).toBeVisible();

    await page.addStyleTag({
      content: '[data-testid="analytics-throughput-header-subtitle"] { font-size: 24px; }',
    });

    const headerHeight = async (testId: string) => {
      const box = await page.getByTestId(testId).boundingBox();
      if (!box) throw new Error(`${testId} has no layout box`);
      return box.height;
    };
    const cardTop = async (testId: string) => {
      const box = await page.getByTestId(testId).boundingBox();
      if (!box) throw new Error(`${testId} has no layout box`);
      return Math.round(box.y);
    };

    // The guard only means something if the headers really do differ in height.
    expect(await headerHeight('analytics-throughput-header')).not.toBe(
      await headerHeight('analytics-aging-header')
    );
    expect(await cardTop('analytics-throughput-stats')).toBe(
      await cardTop('analytics-aging-stats')
    );
  });

  test('item keys in the attention table link back to the item', async ({ page }) => {
    await page.goto(`/workspaces/${workspaceId}/analytics`);

    const attention = page.getByTestId('analytics-attention-table');
    await expect(attention).toBeVisible();

    const firstTitle = attention.locator('table tbody tr').first().locator('button').first();
    await firstTitle.click();

    await expect(page).toHaveURL(new RegExp(`/workspaces/${workspaceId}/items/\\d+`));
  });
});
