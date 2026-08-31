import {
  createCollectionViaAPI,
  createCustomFieldViaAPI,
  deleteCustomFieldViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

/**
 * WI-6: "Collection Filters do not show up when the query involves custom fields"
 *
 * Verification only — proves whether the symptom still reproduces on main.
 *
 * Symptom: Collections.svelte.hydrateFromCollection enters raw mode whenever a
 * saved collection has `ql_query` but no `filter_state`. Raw mode flips
 * `disabled={rawMode}` on WorkItemFilterPanel, which renders the standard
 * pickers (workspace/status/priority) inert (opacity-50, pointer-events-none)
 * and surfaces a "Builder disabled" callout — and there's no Assignee picker
 * at all in the standard set (Assignee is only available through dynamic
 * filters, which the disabled state also blocks).
 *
 * Both specs wait for `loadCollectionById` to actually finish (signaled by
 * the QL bar settling into builder or raw mode) before asserting — without
 * that, the test races the async hydrate and gives a false negative.
 */

const BUILDER_DISABLED_TEXT = /builder disabled/i;

/**
 * Positively assert the filter builder is in interactive builder mode — not
 * merely that the "builder disabled" text happens to be absent. Proves: builder
 * mode is active (not raw), the filter panel is rendered and not dimmed /
 * pointer-events-none, and a real builder control (Add Field Filter) is usable.
 */
async function expectBuilderInteractive(page: import('@playwright/test').Page) {
  await expect(page.getByTestId('ql-enter-raw-mode')).toBeVisible();
  await expect(page.getByTestId('ql-reset-to-builder')).toHaveCount(0);

  const panel = page.getByTestId('collection-filters');
  await expect(panel).toBeVisible();
  await expect(panel).not.toHaveClass(/pointer-events-none/);
  await expect(panel.getByTestId('collection-add-dynamic-filter')).toBeVisible();

  await expect(page.getByText(BUILDER_DISABLED_TEXT)).toHaveCount(0);
}

test.describe('WI-6: collection filter sidebar', () => {
  let customFieldId: number | null = null;
  let customFieldName: string;

  test.beforeAll(async ({ request }) => {
    customFieldName = `wi6_${Date.now()}`;
    const result = await createCustomFieldViaAPI(request, {
      name: customFieldName,
      field_type: 'text',
    });
    customFieldId = result?.id ?? result?.data?.id ?? null;
  });

  test.afterAll(async ({ request }) => {
    if (customFieldId) {
      await deleteCustomFieldViaAPI(request, customFieldId);
    }
  });

  test('collection with custom-field QL keeps builder filters active', async ({
    page,
    request,
  }) => {
    const collection = await createCollectionViaAPI(request, {
      name: `WI-6 cf ${Date.now()}`,
      ql_query: `cf_${customFieldName} = "foo"`,
    });

    await page.goto(`/collections/${collection.id}`);

    // Wait until the QL bar resolves to either raw or builder mode — proves
    // loadCollectionById completed and the store's rawMode is settled.
    await expect(
      page.getByTestId('ql-reset-to-builder').or(page.getByTestId('ql-enter-raw-mode'))
    ).toBeVisible({ timeout: 15000 });

    // The builder must stay interactive for a custom-field query (the WI-6 bug).
    await expectBuilderInteractive(page);
  });

  test('collection with standard-field QL keeps builder filters active', async ({
    page,
    request,
  }) => {
    // Control case: same flow, but the QL does NOT reference a custom field.
    // Distinguishes "bug specific to custom fields" from "any QL-only collection
    // disables the builder".
    const collection = await createCollectionViaAPI(request, {
      name: `WI-6 std ${Date.now()}`,
      ql_query: `status = "Open"`,
    });

    await page.goto(`/collections/${collection.id}`);

    await expect(
      page.getByTestId('ql-reset-to-builder').or(page.getByTestId('ql-enter-raw-mode'))
    ).toBeVisible({ timeout: 15000 });

    // Control case: a standard-field QL-only collection must also stay in
    // interactive builder mode.
    await expectBuilderInteractive(page);
  });
});
