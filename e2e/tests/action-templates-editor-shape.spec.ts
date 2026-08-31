import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import type { Page } from '../fixtures/context-path';
import { expect, test } from '../fixtures/errors';

/**
 * Editor-shape contract for action templates.
 *
 * Companion to action-templates-close-subtasks.spec.ts (which verifies the
 * runtime contract — parent transition closes children). This spec verifies
 * the *UI* contract: after applying a template the action editor opens with
 * the template's nodes and edges already wired, not the empty-default-trigger
 * fallback that the bug fixed alongside this spec produced.
 *
 * Add a new template here by appending one entry to `cases`. The node-type
 * keys must match `node_type` values in the YAML under
 * core/internal/services/actiontemplates/templates/*.yaml.
 */

type TemplateCase = {
  templateKey: string;
  displayName: string;
  expectedNodeTypes: Record<string, number>;
  expectedEdgeCount: number;
};

const cases: TemplateCase[] = [
  {
    templateKey: 'close_subtasks_on_parent_close',
    displayName: 'Close subtasks when parent closes',
    expectedNodeTypes: { trigger: 1, related_items: 1, transition_item: 1 },
    expectedEdgeCount: 2,
  },
];

async function applyTemplateAndOpenEditor(page: Page, workspaceId: number, tc: TemplateCase) {
  await page.goto(`/workspaces/${workspaceId}/actions`);
  await page.getByTestId('actions-from-template').click();

  const modal = page.locator('[role="dialog"]');
  await expect(modal).toBeVisible();
  await modal.getByTestId(`action-template-apply-${tc.templateKey}`).click();

  // Modal closes once apply succeeds; editor mounts in its place.
  await expect(modal).toBeHidden();
}

test.describe('Action templates: editor shape after apply', () => {
  for (const tc of cases) {
    test(`template "${tc.templateKey}" renders correct editor shape`, async ({
      page,
      request,
      allowConsoleError,
    }) => {
      // Optional logbook service emits a 404 on /api/logbook/health when not
      // deployed; established noise pattern, see button-smoke.spec.ts.
      allowConsoleError(/\/api\/logbook\//);

      const stamp = Date.now();
      const ws = await createWorkspaceViaAPI(request, {
        name: `tpl-shape-${tc.templateKey}-${stamp}`,
        key: `T${stamp.toString().slice(-6)}`.toUpperCase(),
        description: 'editor-shape check',
      });

      await applyTemplateAndOpenEditor(page, ws.id, tc);

      const flow = page.locator('.svelte-flow');
      await expect(flow).toBeVisible();

      const expectedTotal = Object.values(tc.expectedNodeTypes).reduce((a, b) => a + b, 0);
      // Wait for SvelteFlow to mount all nodes (it renders progressively as
      // the flow store hydrates from the fetched action).
      await expect(flow.locator('.svelte-flow__node')).toHaveCount(expectedTotal);

      for (const [nodeType, count] of Object.entries(tc.expectedNodeTypes)) {
        await expect(
          flow.locator(`.svelte-flow__node-${nodeType}`),
          `expected ${count} ${nodeType} node(s) for template ${tc.templateKey}`
        ).toHaveCount(count);
      }

      await expect(flow.locator('.svelte-flow__edge')).toHaveCount(tc.expectedEdgeCount);
    });
  }
});
