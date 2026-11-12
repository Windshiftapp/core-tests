import fs from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import type { Page, APIRequestContext } from '../fixtures/context-path';
import { test, expect } from '../fixtures/errors';
import { createWorkspaceViaAPI, createItemViaAPI } from '../fixtures/api-helpers';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

/**
 * Refactor screenshot harness.
 *
 * Pilot tool for the inline-styles → Tailwind refactor. Captures pre/post
 * screenshots of a target component into /tmp/refactor-shots/<target>/ so
 * we can eyeball drift while migrating files one at a time.
 *
 * Usage (via run-e2e.sh):
 *   REFACTOR_PHASE=before REFACTOR_TARGET=WidgetWrapper \
 *     ./core-tests/run-e2e.sh refactor-shots.spec.ts
 *
 * Outputs:
 *   /tmp/refactor-shots/<target>/<phase>-page.png      (full-page)
 *   /tmp/refactor-shots/<target>/<phase>-element.png   (selector-scoped)
 *
 * NOT a permanent regression test — it's a workflow tool. Removed at the
 * end of the refactor effort.
 */

const PHASE = (process.env.REFACTOR_PHASE ?? '').toLowerCase();
const TARGET = process.env.REFACTOR_TARGET ?? '';
const OUT_BASE = process.env.REFACTOR_OUT ?? '/tmp/refactor-shots';

interface Ctx {
  page: Page;
  request: APIRequestContext;
}

interface Target {
  name: string;
  navigate: (ctx: Ctx) => Promise<void>;
  selector: string;
}

let cachedWorkspaceId: number | null = null;
async function ensurePilotWorkspace(request: APIRequestContext): Promise<number> {
  if (cachedWorkspaceId !== null) return cachedWorkspaceId;
  const created = await createWorkspaceViaAPI(request, {
    name: 'CSS Pilot',
    key: `CSSP${Date.now().toString().slice(-5)}`,
    description: 'workspace used by refactor-shots harness',
  });
  cachedWorkspaceId = created.id;
  return created.id;
}

async function gotoWorkspaceWelcome({ page, request }: Ctx) {
  const wsId = await ensurePilotWorkspace(request);
  // /workspaces/{id} redirects to /board; the welcome (with default widgets:
  // stats, completion-chart, created-chart, milestone-progress) is served
  // from /overview.
  await page.goto(`/workspaces/${wsId}/overview`);
  await page.waitForLoadState('networkidle');
}

async function seedRecentItemsLayout(request: APIRequestContext, wsId: number) {
  const sectionId = randomUUID();
  const widgetId = randomUUID();
  const res = await request.put(`${BASE_URL}/api/workspaces/${wsId}/homepage/layout`, {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
    data: {
      sections: [{
        id: sectionId,
        title: 'Recent',
        subtitle: '',
        display_order: 0,
        widget_ids: [widgetId],
      }],
      widgets: [{
        id: widgetId,
        type: 'recent-items',
        section_id: sectionId,
        position: 0,
        width: 3,
        config: {},
      }],
      gradient: 0,
      applyToAllViews: false,
      backgroundImageUrl: '',
    },
  });
  expect(res.ok(), `seedRecentItemsLayout failed: ${res.status()}`).toBeTruthy();
}

const TARGETS: Record<string, Target> = {
  WidgetWrapper: {
    name: 'WidgetWrapper',
    // Workspace welcome reliably renders widgets wrapped in WidgetWrapper;
    // the global homepage starts empty for a fresh admin (onboarding panel).
    navigate: gotoWorkspaceWelcome,
    selector: '[data-widget-wrapper]',
  },
  RecentItemsWidget: {
    name: 'RecentItemsWidget',
    navigate: async ({ page, request }) => {
      const wsId = await ensurePilotWorkspace(request);
      await seedRecentItemsLayout(request, wsId);
      // Populate the workspace so the widget renders rows (the data-testid
      // sits on the items list, not on the empty state — and an empty
      // state isn't visually interesting for the refactor anyway).
      // Stable titles keep before/after pixel-comparable.
      for (const title of ['Pilot item alpha', 'Pilot item bravo', 'Pilot item charlie']) {
        await createItemViaAPI(request, wsId, { title, description: 'fixture' });
      }
      await page.goto(`/workspaces/${wsId}/overview`);
      await page.waitForLoadState('networkidle');
    },
    selector: '[data-testid="recent-items-widget"]',
  },
};

test.describe.configure({ mode: 'serial' });

test.describe('refactor-shots', () => {
  test.skip(!PHASE || !TARGET, 'set REFACTOR_PHASE and REFACTOR_TARGET to use this harness');

  test(`capture ${TARGET} (${PHASE})`, async ({ page, request, allowConsoleError }) => {
    // Same environmental noise allowlist as button-smoke: the optional
    // attachment-settings backend 404s in dev, plus rate-limit retries.
    allowConsoleError(/Failed to load resource.*429 \(Too Many Requests\)/);
    allowConsoleError(/\/api\/attachment-settings\/status/);
    allowConsoleError(/Failed to load attachment status/);

    expect(['before', 'after']).toContain(PHASE);
    const target = TARGETS[TARGET];
    expect(target, `unknown REFACTOR_TARGET=${TARGET}`).toBeDefined();

    const dir = path.join(OUT_BASE, target.name);
    fs.mkdirSync(dir, { recursive: true });

    await target.navigate({ page, request });

    const locator = page.locator(target.selector).first();
    await locator.waitFor({ state: 'visible', timeout: 15_000 });

    const pagePath = path.join(dir, `${PHASE}-page.png`);
    const elementPath = path.join(dir, `${PHASE}-element.png`);

    await page.screenshot({ path: pagePath, fullPage: true, animations: 'disabled' });
    await locator.screenshot({ path: elementPath, animations: 'disabled' });

    // eslint-disable-next-line no-console
    console.log(`[refactor-shots] wrote ${pagePath} and ${elementPath}`);
  });
});
