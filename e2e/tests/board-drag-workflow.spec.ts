import {
  authenticateAdminRequest,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import {
  type APIRequestContext,
  expect,
  type Locator,
  type Page,
  test,
} from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';

/**
 * Board drag/drop with workflow + permission enforcement.
 *
 * The board (CollectionBoard.svelte) is wired up via @atlaskit/pragmatic-drag-
 * and-drop. A real DnD library swap or hitbox-detection regression would slip
 * past every API test in the suite — this spec runs at least one real
 * drag-and-drop interaction through Playwright's mouse so the wiring is
 * exercised end-to-end.
 *
 * The backend-only transition validation and permission contracts live in
 * tests/e2e_security_contracts_test.go; this spec keeps the browser workflow
 * and the rejected-drop rollback that only the board UI can exercise.
 *
 * Test plan: docs/e2e-complex-scenario-test-plan.md §2.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface WorkspaceStatus {
  id: number;
  name: string;
  category_id?: number;
}

async function listWorkspaceStatuses(
  request: APIRequestContext,
  workspaceId: number
): Promise<WorkspaceStatus[]> {
  const resp = await request.get(`/api/workspaces/${workspaceId}/statuses`, {
    headers: SEC_FETCH,
  });
  expect(resp.ok(), `list workspace statuses failed (${resp.status()})`).toBeTruthy();
  const body = await resp.json();
  return (body.data ?? body) as WorkspaceStatus[];
}

async function getItem(
  request: APIRequestContext,
  itemId: number
): Promise<{ id: number; status_id: number | null }> {
  const resp = await request.get(`/api/items/${itemId}`, {
    headers: SEC_FETCH,
  });
  expect(resp.ok(), `get item ${itemId} failed (${resp.status()})`).toBeTruthy();
  return resp.json();
}

// Drive a board drag with the same pointer gesture a user performs. The board
// uses Pragmatic DnD's pointer monitor, so moving through the target instead of
// dispatching synthetic drag events also exercises hitbox detection and the
// library's registration lifecycle.
async function dragCardToColumn(page: Page, card: Locator, target: Locator) {
  const cardBox = await card.boundingBox();
  const targetBox = await target.boundingBox();
  if (!cardBox || !targetBox) {
    throw new Error('Board drag source and target must both be visible');
  }

  const startX = cardBox.x + cardBox.width / 2;
  const startY = cardBox.y + cardBox.height / 2;
  const targetX = targetBox.x + targetBox.width / 2;
  const targetY = targetBox.y + Math.min(targetBox.height / 2, 160);
  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(startX + 12, startY + 12);
  await page.mouse.move(targetX, targetY, { steps: 12 });
  await page.mouse.up();
}

test.describe('Board drag/drop with workflow + permission', () => {
  test('dragging at the viewport edge scrolls the board to the rightmost column', {
    tag: '@critical-browser',
  }, async ({ page, request }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 800, height: 720 });
    await authenticateAdminRequest(request);

    const suffix = `bds${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const statuses = await listWorkspaceStatuses(request, ws.id);
    expect(statuses.length).toBeGreaterThanOrEqual(3);

    const itemData = generateItem(ws.id, suffix);
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: itemData.description,
    });

    await page.goto(`/workspaces/${ws.id}/board`);
    await expect(page.getByTestId('board-view')).toBeVisible({
      timeout: 15_000,
    });

    const card = page.getByTestId(`board-item-${item.id}`);
    await expect(card).toHaveAttribute('draggable', 'true', {
      timeout: 5_000,
    });

    const rightmostColumn = page.locator(`#board-column-status-${statuses.at(-1)?.id}`);
    const initialTargetBox = await rightmostColumn.boundingBox();
    expect(initialTargetBox).not.toBeNull();
    expect(initialTargetBox?.x).toBeGreaterThan(800);

    const cardBox = await card.boundingBox();
    if (!cardBox) throw new Error('Board drag source must be visible');
    const startX = cardBox.x + cardBox.width / 2;
    const startY = cardBox.y + cardBox.height / 2;
    await page.mouse.move(startX, startY);
    await page.mouse.down();
    await page.mouse.move(startX + 12, startY + 12);
    await page.mouse.move(792, startY + 12, { steps: 12 });

    await expect
      .poll(async () => {
        const box = await rightmostColumn.boundingBox();
        return box ? box.x + box.width / 2 : 800;
      })
      .toBeLessThan(792);

    await page.mouse.up();
  });

  test('UI drag from one column to another transitions the item via /items/{id}/transition', {
    tag: '@critical-browser',
  }, async ({ page, request }) => {
    test.setTimeout(60_000);
    await authenticateAdminRequest(request);

    const suffix = `bdg${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const statuses = await listWorkspaceStatuses(request, ws.id);
    // The board needs at least two columns to drag between. The seeded
    // workflow on a fresh workspace ships three (To Do / In Progress / Done)
    // — assert before scripting the drag so the failure is clear if seeding
    // ever changes.
    expect(
      statuses.length,
      `workspace ${ws.id} needs ≥2 statuses for drag test, got ${statuses.length}`
    ).toBeGreaterThanOrEqual(2);
    const fromStatus = statuses[0];
    const toStatus = statuses[1];

    // Create the item in the first column. createItemViaAPI accepts a `status`
    // name string and the backend resolves it — but on a fresh workspace the
    // item lands in the workflow's initial status anyway, which is `fromStatus`.
    const itemData = generateItem(ws.id, suffix);
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: itemData.description,
    });

    await page.goto(`/workspaces/${ws.id}/board`);
    await expect(page.locator('[data-testid="board-view"]')).toBeVisible({
      timeout: 15_000,
    });

    const card = page.getByTestId(`board-item-${item.id}`);
    await expect(card).toBeVisible({ timeout: 10_000 });

    const sourceColumn = page.locator(`#board-column-status-${fromStatus.id}`);
    const targetColumn = page.locator(`#board-column-status-${toStatus.id}`);
    await expect(sourceColumn).toBeVisible();
    await expect(targetColumn).toBeVisible();

    // setupDragAndDrop() in CollectionBoard.svelte wires the cards via a
    // $effect that debounces by 100ms (`setTimeout(..., 100)` near line 762).
    // Until that fires, pragmatic-drag-and-drop hasn't registered the card as
    // a draggable — dispatching `dragstart` on it is a silent no-op because
    // the library's adapter rejects sources that aren't in its registry. The
    // `draggable="true"` attribute is the library's own registration signal.
    await expect(card).toHaveAttribute('draggable', 'true', {
      timeout: 5_000,
    });

    await dragCardToColumn(page, card, targetColumn);

    // Backend persistence — the board calls api.items.transition() on drop,
    // which is POST /items/{id}/transition with `to_status_id`. Poll the
    // item until the new status sticks (the request runs after the drop
    // animation, hence the small wait window).
    await expect
      .poll(
        async () => {
          const current = await getItem(request, item.id);
          return current.status_id;
        },
        {
          message: `item ${item.id} did not transition to status ${toStatus.id} after drag`,
          timeout: 15_000,
        }
      )
      .toBe(toStatus.id);

    // UI side — the card should now be a child of the target column. We
    // don't assert it left the original column (the source can re-render
    // either way during the reload) but the card MUST be visible inside
    // the target column.
    await expect(targetColumn.getByTestId(`board-item-${item.id}`)).toBeVisible({
      timeout: 10_000,
    });
  });

  test('UI drag optimistically moves then restores a workflow-rejected transition', async ({
    page,
    request,
  }) => {
    test.setTimeout(60_000);
    await authenticateAdminRequest(request);

    const suffix = `bdo${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const statuses = await listWorkspaceStatuses(request, ws.id);
    expect(statuses.length).toBeGreaterThanOrEqual(3);
    const targetStatus = statuses.find((status) => status.name === 'In Progress');
    const sourceStatus = statuses.find((status) => status.name === 'Done');
    expect(targetStatus, 'default workflow needs In Progress').toBeDefined();
    expect(sourceStatus, 'default workflow needs Done').toBeDefined();

    const itemData = generateItem(ws.id, suffix);
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: itemData.description,
    });
    const moveToDone = await request.post(`/api/items/${item.id}/transition`, {
      headers: SEC_FETCH,
      data: { to_status_id: sourceStatus?.id },
    });
    expect(
      moveToDone.ok(),
      `fixture transition to Done failed (${moveToDone.status()})`
    ).toBeTruthy();

    let markTransitionRequested: (() => void) | undefined;
    const transitionRequested = new Promise<void>((resolve) => {
      markTransitionRequested = resolve;
    });
    let releaseTransition: (() => void) | undefined;
    const transitionReleased = new Promise<void>((resolve) => {
      releaseTransition = resolve;
    });
    await page.route(`**/api/items/${item.id}/transition`, async (route) => {
      markTransitionRequested?.();
      await transitionReleased;
      await route.continue();
    });

    await page.goto(`/workspaces/${ws.id}/board`);
    await expect(page.getByTestId('board-view')).toBeVisible({
      timeout: 15_000,
    });

    const card = page.getByTestId(`board-item-${item.id}`);
    const sourceColumn = page.locator(`#board-column-status-${sourceStatus?.id}`);
    const targetColumn = page.locator(`#board-column-status-${targetStatus?.id}`);
    await expect(card).toHaveAttribute('draggable', 'true', {
      timeout: 5_000,
    });

    await dragCardToColumn(page, card, targetColumn);
    await transitionRequested;

    // Keep the request at the browser boundary long enough to prove the
    // card moves before the server has accepted the transition.
    await expect(targetColumn.getByTestId(`board-item-${item.id}`)).toBeVisible();

    const rejectedResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/items/${item.id}/transition`) &&
        response.request().method() === 'POST'
    );
    releaseTransition?.();
    expect((await rejectedResponse).status()).toBeGreaterThanOrEqual(400);

    await expect(sourceColumn.getByTestId(`board-item-${item.id}`)).toBeVisible({
      timeout: 10_000,
    });
    expect((await getItem(request, item.id)).status_id).toBe(sourceStatus?.id);
  });
});
