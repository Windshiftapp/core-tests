import { expect, test } from '../fixtures/context-path';

/**
 * WI-961: the token scope picker renders the server scope catalog
 * (GET /api-tokens/scope-catalog) instead of a hand-maintained list.
 *
 * The list it replaced had drifted badly: time:read/time:write were absent
 * entirely, which is how an MCP agent ended up unable to log time, and
 * mcp:access existed as a row but sat in a fixed read/write/delete column
 * layout that had no column to render it in, so it could never be ticked.
 */

test.describe('Token scope picker', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/security');
    await expect(page.getByTestId('security-page')).toBeVisible();

    await page.keyboard.press('t');
    // The picker only has content once the catalog request resolves.
    await expect(page.getByTestId('token-scope-picker')).toBeVisible();
  });

  test('offers the scopes that were previously ungrantable', async ({ page }) => {
    for (const scope of [
      'time:read',
      'time:write',
      'time:delete',
      'mcp:access',
      'actions:read',
      'actions:write',
      'tests:read',
      'assets:read',
      'agent-skills:read',
    ]) {
      await expect(page.getByTestId(`token-scope-${scope}`)).toBeVisible();
    }
  });

  test('"Agent default" preset selects the MCP-capable scope set', async ({ page }) => {
    await page.locator('#token-name').fill('agent-default-token');
    await page.getByTestId('token-scope-preset-agent-default').click();

    const created = page.waitForResponse(
      (response) => response.url().includes('/api-tokens') && response.request().method() === 'POST'
    );
    await page.getByTestId('create-token-submit').click();

    const response = await created;
    expect(response.status(), 'token creation should succeed').toBeLessThan(400);

    const sent = JSON.parse(response.request().postData() ?? '{}');
    expect(sent.permissions).toEqual(
      expect.arrayContaining([
        'time:read',
        'time:write',
        'mcp:access',
        'items:write',
        'actions:write',
      ])
    );
    expect(sent.permissions).not.toEqual(
      expect.arrayContaining(['items:delete', 'time:delete', 'workspaces:delete'])
    );
  });

  test('creates a token carrying time scopes', async ({ page }) => {
    await page.locator('#token-name').fill('mcp-time-token');

    await page.getByTestId('token-scope-preset-agent-default').click();

    const created = page.waitForResponse(
      (r) => r.url().includes('/api-tokens') && r.request().method() === 'POST'
    );
    await page.getByTestId('create-token-submit').click();

    const response = await created;
    expect(response.status(), 'token creation should succeed').toBeLessThan(400);

    const sent = JSON.parse(response.request().postData() ?? '{}');
    expect(sent.permissions).toContain('time:read');
    expect(sent.permissions).toContain('time:write');
    expect(sent.permissions).toContain('mcp:access');
    expect(sent.permissions).not.toContain('items:delete');
  });

  test('sends the selected expiration as a calendar date', async ({ page }) => {
    await page.locator('#token-name').fill('dated-token');
    await page.locator('#token-expiry').fill('2030-06-15');

    const created = page.waitForResponse(
      (response) => response.url().includes('/api-tokens') && response.request().method() === 'POST'
    );
    await page.getByTestId('create-token-submit').click();

    const response = await created;
    expect(response.status(), 'token creation should succeed').toBeLessThan(400);
    expect(JSON.parse(response.request().postData() ?? '{}').expires_on).toBe('2030-06-15');
  });
});
