import assert from 'node:assert/strict';
import test from 'node:test';

import { selectorViolations, violationCounts } from './selector-policy.mjs';

test('accepts test IDs and exact ID selectors', () => {
  const violations = selectorViolations(`
		page.getByTestId("save");
		page.locator("#email");
		page.locator(\`#mobile-item-row-\${item.id}\`);
		page.locator('[data-testid="dialog-confirm"]');
	`);

  assert.deepEqual(violations, []);
});

test('reports translated queries and presentation selectors', () => {
  const violations = selectorViolations(`
		page.getByRole("button", { name: "Save" });
		page.getByText("Saved");
		page.getByTitle("Close");
		page.locator(".dialog button");
		page.waitForSelector("div[role=dialog]");
		page.locator("[data-testid=row]").filter({ hasText: title });
		page.fill("input[name=email]", "me@example.com");
		page.getByTestId("email").fill("me@example.com");
	`);

  assert.deepEqual(violationCounts(violations), {
    'filter.hasText': 1,
    getByRole: 1,
    getByText: 1,
    getByTitle: 1,
    'fill.unstable': 1,
    'locator.unstable': 1,
    'waitForSelector.unstable': 1,
  });
  assert.equal(violations[0].line, 2);
});

test('requires dynamic locators to be reviewed', () => {
  const violations = selectorViolations('page.locator(selector);');
  assert.deepEqual(violationCounts(violations), { 'locator.dynamic': 1 });
});
