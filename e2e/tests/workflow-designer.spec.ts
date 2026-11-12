import { type APIRequestContext, expect, test } from '../fixtures/context-path';

/**
 * Workflow designer UI round-trip.
 *
 * The designer (frontend/src/lib/features/workflows/SvelteFlowDesigner.svelte)
 * is an XYFlow canvas with a status palette on the left and Save/Cancel
 * controls overlaid on the canvas. Existing specs only exercise the
 * workflow transition API; this one drives the visual designer:
 *
 *   - Loads workflow + global status list via API.
 *   - Renders the palette, the canvas, and one status node per existing
 *     workflow transition.
 *   - Clicking a palette card calls addStatusToWorkflow() — this adds a
 *     SvelteFlow node and (on save) emits an addPreservationTransitions
 *     self-loop so the status is persisted as part of the workflow.
 *   - Save fires PUT /workflows/{id}/transitions with the full transition
 *     set.
 *
 * Scope notes (test plan §3 deliberately scaled back):
 *   - Edge-drawing via handle drag is NOT exercised. XYFlow handles only
 *     appear on hover and the drag-to-connect path is timing-sensitive in
 *     the Playwright harness — exercising it here would trade real
 *     coverage for flake. The persistence + reload assertions still pin
 *     "graph persisted" by reading back the saved transitions.
 *   - Condition-set editing happens in a different surface; the test plan
 *     covered them in condition-sets.spec.ts already.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface Status {
  id: number;
  name: string;
  category_id: number;
}

interface Workflow {
  id: number;
  name: string;
}

interface WorkflowTransition {
  id: number;
  from_status_id: number | null;
  to_status_id: number;
  from_all_statuses?: boolean;
}

async function createStatus(
  request: APIRequestContext,
  name: string,
  categoryId: number
): Promise<Status> {
  const resp = await request.post('/api/v2/statuses', {
    headers: SEC_FETCH,
    data: { name, category_id: categoryId },
  });
  expect(resp.ok(), `create status ${name}: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  return (await resp.json()).data;
}

async function createWorkflow(request: APIRequestContext, name: string): Promise<Workflow> {
  const resp = await request.post('/api/v2/workflows', {
    headers: SEC_FETCH,
    data: { name, description: 'e2e workflow designer' },
  });
  expect(resp.ok(), `create workflow ${name}: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  return (await resp.json()).data;
}

function normalizeTransition(transition: {
  id: number;
  from: { id: number } | null;
  to: { id: number };
  from_all_statuses?: boolean;
}): WorkflowTransition {
  return {
    id: transition.id,
    from_status_id: transition.from?.id ?? null,
    to_status_id: transition.to.id,
    from_all_statuses: transition.from_all_statuses,
  };
}

async function setTransitions(
  request: APIRequestContext,
  workflowId: number,
  transitions: Array<Partial<WorkflowTransition>>
): Promise<WorkflowTransition[]> {
  const resp = await request.put(`/api/v2/workflows/${workflowId}/transitions`, {
    headers: SEC_FETCH,
    data: { transitions },
  });
  expect(resp.ok(), `seed transitions: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  return (await resp.json()).data.map(normalizeTransition);
}

async function setInitialTransition(
  request: APIRequestContext,
  workflowId: number,
  initialStatusId: number
): Promise<WorkflowTransition[]> {
  // PUT /workflows/{id}/transitions replaces the full transition set.
  // A NULL from_status_id pins the workflow's "initial" status, which the
  // designer renders as a special node and uses to seed the palette →
  // workspace handoff.
  const resp = await request.put(`/api/v2/workflows/${workflowId}/transitions`, {
    headers: SEC_FETCH,
    data: { transitions: [{ from_status_id: null, to_status_id: initialStatusId }] },
  });
  expect(resp.ok(), `seed transitions: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  return (await resp.json()).data.map(normalizeTransition);
}

async function defaultCategoryId(request: APIRequestContext): Promise<number> {
  const resp = await request.get('/api/v2/status-categories', { headers: SEC_FETCH });
  expect(resp.ok()).toBeTruthy();
  const cats: Array<{ id: number; is_default: boolean }> = (await resp.json()).data;
  const def = cats.find((c) => c.is_default) ?? cats[0];
  expect(def, 'no status categories seeded').toBeTruthy();
  return def.id;
}

async function getWorkflow(
  request: APIRequestContext,
  workflowId: number
): Promise<Workflow & { transitions?: WorkflowTransition[] }> {
  const [workflowResponse, transitionsResponse] = await Promise.all([
    request.get(`/api/v2/workflows/${workflowId}`, { headers: SEC_FETCH }),
    request.get(`/api/v2/workflows/${workflowId}/transitions`, { headers: SEC_FETCH }),
  ]);
  expect(workflowResponse.ok(), `get workflow: ${workflowResponse.status()}`).toBeTruthy();
  expect(transitionsResponse.ok(), `get transitions: ${transitionsResponse.status()}`).toBeTruthy();
  const workflow = (await workflowResponse.json()).data;
  const transitions = (await transitionsResponse.json()).data.map(normalizeTransition);
  return { ...workflow, transitions };
}

test.describe('Workflow designer UI round-trip', () => {
  test('palette adds + save persists; designer reload renders the same graph', async ({
    page,
    request,
  }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const prefix = `wfd-${stamp}`;
    const categoryId = await defaultCategoryId(request);

    // Three statuses + a workflow seeded with one initial transition. We
    // start with one status in the workflow (Todo) and add two more via
    // the designer's palette below.
    const todo = await createStatus(request, `${prefix}-Todo`, categoryId);
    const inProgress = await createStatus(request, `${prefix}-InProgress`, categoryId);
    const done = await createStatus(request, `${prefix}-Done`, categoryId);
    const workflow = await createWorkflow(request, `${prefix}-wf`);
    await setInitialTransition(request, workflow.id, todo.id);

    // Navigate to the designer. The route is /workflows/{id}/design and
    // resolves to WorkflowDesigner.svelte (router.js:129). The designer
    // does dynamic-import of SvelteFlowDesigner, so wait for the canvas
    // class that the underlying SvelteFlow component renders.
    await page.goto(`/workflows/${workflow.id}/design`);
    await expect(page.getByTestId('workflow-canvas')).toBeVisible({ timeout: 20_000 });

    // The palette is the left sidebar — its buttons are the three statuses
    // not yet in the workflow. Todo is already in the workflow (initial
    // transition), so only InProgress + Done appear here.
    const inProgressCard = page.getByTestId(`workflow-status-option-${inProgress.id}`);
    const doneCard = page.getByTestId(`workflow-status-option-${done.id}`);
    await expect(inProgressCard).toBeVisible({ timeout: 10_000 });
    await expect(doneCard).toBeVisible();

    // The canvas already has one node — the initial Todo. SvelteFlow nodes
    // render with id `status-{statusId}` (SvelteFlowDesigner.svelte:262).
    await expect(page.getByTestId(`workflow-status-node-${todo.id}`)).toBeVisible({
      timeout: 10_000,
    });

    // Add InProgress + Done from the palette. addStatusToWorkflow() pushes
    // a SvelteFlow node, which materialises in the canvas; once saved a
    // self-preservation transition is appended so the status sticks even
    // without any user-drawn edges.
    await inProgressCard.click();
    await expect(page.getByTestId(`workflow-status-node-${inProgress.id}`)).toBeVisible({
      timeout: 10_000,
    });

    await doneCard.click();
    await expect(page.getByTestId(`workflow-status-node-${done.id}`)).toBeVisible({
      timeout: 10_000,
    });

    // Save. The "Save Workflow" button is the rightmost overlay control;
    // its label comes from t('workflows.saveWorkflow'). We wait on the
    // outgoing PUT so the assertion below reads post-save state.
    const savePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/v2/workflows/${workflow.id}/transitions`) &&
        resp.request().method() === 'PUT'
    );
    await page.getByTestId('workflow-save').click();
    const saveResp = await savePromise;
    expect(saveResp.ok(), `save: ${saveResp.status()}`).toBeTruthy();

    // After save, WorkflowDesigner.svelte navigates back to /admin/workflows
    // (handleSave at line 60). Don't assert on that URL — the designer's
    // post-save behaviour is incidental. The persistence assertion below
    // is the contract.
    const saved = await getWorkflow(request, workflow.id);
    const savedStatusIds = new Set<number>();
    for (const tx of saved.transitions ?? []) {
      if (tx.from_status_id != null) savedStatusIds.add(tx.from_status_id);
      savedStatusIds.add(tx.to_status_id);
    }
    expect(savedStatusIds.has(todo.id), 'Todo lost from workflow').toBe(true);
    expect(savedStatusIds.has(inProgress.id), 'InProgress not persisted').toBe(true);
    expect(savedStatusIds.has(done.id), 'Done not persisted').toBe(true);

    // Reload the designer to confirm the saved graph renders. This pins
    // the "graph persisted across reload" test-plan invariant — a broken
    // serialization in edgesToTransitions / transitionsToEdges would lose
    // one of the nodes here even though the API round-tripped.
    await page.goto(`/workflows/${workflow.id}/design`);
    await expect(page.getByTestId('workflow-canvas')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId(`workflow-status-node-${todo.id}`)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByTestId(`workflow-status-node-${inProgress.id}`)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByTestId(`workflow-status-node-${done.id}`)).toBeVisible({
      timeout: 10_000,
    });
  });

  test('all-statuses checkbox persists a from-all transition and renders the special arrow', async ({
    page,
    request,
  }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const prefix = `wfd-all-${stamp}`;
    const categoryId = await defaultCategoryId(request);

    const open = await createStatus(request, `${prefix}-Open`, categoryId);
    const inProgress = await createStatus(request, `${prefix}-InProgress`, categoryId);
    const reopened = await createStatus(request, `${prefix}-Reopened`, categoryId);
    const workflow = await createWorkflow(request, `${prefix}-wf`);
    await setTransitions(request, workflow.id, [
      { from_status_id: null, to_status_id: open.id },
      { from_status_id: open.id, to_status_id: inProgress.id },
      { from_status_id: inProgress.id, to_status_id: open.id },
      { from_status_id: inProgress.id, to_status_id: reopened.id },
    ]);

    await page.goto(`/workflows/${workflow.id}/design`);
    await expect(page.getByTestId('workflow-canvas')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId(`workflow-status-node-${open.id}`)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByTestId(`workflow-status-node-${inProgress.id}`)).toBeVisible({
      timeout: 10_000,
    });

    // Reopened is already in the seeded graph, so the designer lays it out
    // together with the other nodes before the interaction begins.
    const reopenedNode = page.getByTestId(`workflow-status-node-${reopened.id}`);
    await expect(reopenedNode).toBeVisible({ timeout: 10_000 });

    // Bring the reopened node's toggle to the canvas center. The graph's
    // fit-view/layout can still be settling under load, so compute the pan
    // from live bounding boxes instead of a fixed delta — a blind pan can
    // park the toggle under the sidebar. The drag starts in the canvas
    // corner so it pans the viewport rather than dragging a node.
    const canvas = await page.getByTestId('workflow-canvas').boundingBox();
    if (!canvas) throw new Error('Workflow canvas is not rendered');
    const allToggle = reopenedNode.getByTestId('status-all-toggle');
    const toggleBox = await allToggle.boundingBox();
    if (!toggleBox) throw new Error('Status all-toggle is not rendered');
    const panX = canvas.x + canvas.width / 2 - toggleBox.width / 2 - toggleBox.x;
    const panY = canvas.y + canvas.height / 2 - toggleBox.height / 2 - toggleBox.y;
    if (Math.abs(panX) > 2 || Math.abs(panY) > 2) {
      await page.mouse.move(canvas.x + 20, canvas.y + 20);
      await page.mouse.down();
      await page.mouse.move(canvas.x + 20 + panX, canvas.y + 20 + panY, {
        steps: 10,
      });
      await page.mouse.up();
    }

    // Tick the per-status "All" checkbox. The chip lives inside the node and
    // carries aria-checked so the checked state is assertable without
    // relying on styling.
    await expect(allToggle).toHaveAttribute('aria-checked', 'false');
    await allToggle.click();
    await expect(allToggle).toHaveAttribute('aria-checked', 'true');

    // The single special arrow replaces N incoming arrows: one self-loop
    // edge with the stable id edge-all-{statusId}.
    const allEdge = page.getByTestId(`workflow-edge-all-${reopened.id}`);
    await expect(allEdge).toBeVisible({ timeout: 10_000 });

    // Save and verify the persisted row through the API response.
    const savePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/v2/workflows/${workflow.id}/transitions`) &&
        resp.request().method() === 'PUT'
    );
    await page.getByTestId('workflow-save').click();
    const saveResp = await savePromise;
    expect(saveResp.ok(), `save: ${saveResp.status()}`).toBeTruthy();

    const saved = await getWorkflow(request, workflow.id);
    const fromAll = (saved.transitions ?? []).filter((tx) => tx.from_all_statuses);
    expect(fromAll).toHaveLength(1);
    expect(fromAll[0].from_status_id).toBeNull();
    expect(fromAll[0].to_status_id).toBe(reopened.id);

    // Reload: the checkbox stays ticked and the special arrow re-renders
    // from the persisted transition.
    await page.goto(`/workflows/${workflow.id}/design`);
    await expect(page.getByTestId('workflow-canvas')).toBeVisible({ timeout: 20_000 });
    const reloadedNode = page.getByTestId(`workflow-status-node-${reopened.id}`);
    await expect(reloadedNode).toBeVisible({ timeout: 10_000 });
    await expect(reloadedNode.getByTestId('status-all-toggle')).toHaveAttribute(
      'aria-checked',
      'true'
    );
    await expect(page.getByTestId(`workflow-edge-all-${reopened.id}`)).toBeVisible({
      timeout: 10_000,
    });
  });

  test('clicking a transition edge opens the inspector with its controls in view', async ({
    page,
    request,
  }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const prefix = `wfd-inspector-${stamp}`;
    const categoryId = await defaultCategoryId(request);

    const from = await createStatus(request, `${prefix}-From`, categoryId);
    const to = await createStatus(request, `${prefix}-To`, categoryId);
    const workflow = await createWorkflow(request, `${prefix}-wf`);
    const transitions = await setTransitions(request, workflow.id, [
      { from_status_id: null, to_status_id: from.id },
      { from_status_id: from.id, to_status_id: to.id },
    ]);
    const edge = transitions.find(
      (transition) => transition.from_status_id === from.id && transition.to_status_id === to.id
    );
    expect(edge, 'from -> to transition not created').toBeTruthy();

    // Bind a screen through the API (setup), so the inspector renders its
    // bound-state controls. The name is deliberately long: the picker's widest
    // option drives its min-content width, which is what pushed the Unbind
    // button off screen.
    const screenName = `${prefix}-Support Ticket Resolution Screen With A Long Name`;
    const screenResp = await request.post('/api/v2/screens', {
      headers: SEC_FETCH,
      data: { name: screenName, description: 'e2e transition screen' },
    });
    expect(
      screenResp.ok(),
      `create screen: ${screenResp.status()} ${await screenResp.text()}`
    ).toBeTruthy();
    const screen = (await screenResp.json()).data;
    const bindResp = await request.put(`/api/v2/transitions/${edge!.id}/screen`, {
      headers: SEC_FETCH,
      data: { screen_id: screen.id },
    });
    expect(bindResp.ok(), `bind screen: ${bindResp.status()} ${await bindResp.text()}`).toBeTruthy();

    await page.goto(`/workflows/${workflow.id}/design`);
    await expect(page.getByTestId('workflow-canvas')).toBeVisible({ timeout: 20_000 });

    // A narrow laptop viewport: palette + canvas + inspector must still fit, or
    // the inspector's right-hand controls get pushed off screen.
    await page.setViewportSize({ width: 1024, height: 768 });

    // The edge carries a stable test id derived from the persisted transition
    // id. Dispatch the click so the assertion is about the inspector, not
    // about hitting a one-pixel SVG stroke.
    await page.getByTestId(`workflow-edge-${edge!.id}`).dispatchEvent('click');

    await expect(page.getByTestId('transition-inspector')).toBeVisible();
    await expect(page.getByTestId('transition-inspector-screen-name')).toHaveText(screenName);

    // The canvas must not push the inspector's right-hand controls off the
    // viewport (WI-1835 follow-up).
    await expect(page.getByTestId('transition-inspector-unbind')).toBeInViewport();
    await expect(page.getByTestId('transition-inspector-create-screen')).toBeInViewport();

    // The screen's fields are editable from the inspector, not just on the
    // Screens admin page.
    await page.getByTestId('transition-inspector-edit-fields').click();
    await expect(page.getByTestId('screen-field-layout-editor')).toBeVisible();
  });
});
