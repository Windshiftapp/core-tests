import { expect, test } from '../fixtures/context-path';

/**
 * WI-77 follow-up: the OpenAPI spec must be served publicly at
 * /rest/api/v1/openapi.{json,yaml}, and the SPA must mount Scalar at
 * /api-docs pointed at that JSON spec.
 */

// The public JSON/YAML endpoint contracts are covered by the HTTP-layer tests
// in internal/restapi/v1/handlers/openapi_test.go. This browser spec owns the
// user-visible API documentation page only.

test.describe('/api-docs page', () => {
  test('the /api-docs route fetches the spec and renders the native viewer', async ({ page }) => {
    const specRequest = page.waitForRequest(
      (req) => req.url().includes('/rest/api/v1/openapi.json') && req.method() === 'GET'
    );

    await page.goto('/api-docs');
    await page.waitForLoadState('networkidle');

    // The page must fetch the embedded spec from the v1 endpoint.
    const req = await specRequest;
    expect(req).toBeTruthy();

    // The native sidebar lists tag groups with at least one operation row.
    await expect(page.getByTestId('api-docs-sidebar')).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByTestId('api-docs-op-link').first()).toBeVisible();

    // The main panel renders a selected operation panel.
    await expect(page.getByTestId('api-docs-operation').first()).toBeVisible();
  });

  test('documents the public metrics endpoint', async ({ page }) => {
    await page.goto('/api-docs');
    await page.waitForLoadState('networkidle');

    await page.getByTestId('api-docs-filter').fill('metrics');
    const result = page.getByTestId('api-docs-op-link');
    await expect(result).toHaveCount(1);
    await result.click();

    await expect(page.getByTestId('api-docs-operation')).toHaveAttribute('data-path', '/metrics');
  });
});
