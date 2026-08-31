import {
  createCollectionViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

test('keeps roadmap settings visible when an item has an old due date', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Roadmap overdue ${stamp}`,
    key: `RO${stamp.toString().slice(-6)}`.toUpperCase(),
    description: 'Playwright roadmap overflow coverage',
  });
  const itemResponse = await request.post('/api/items', {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
    data: {
      workspace_id: workspace.id,
      title: `Long overdue delivery ${stamp}`,
      due_date: '2000-01-01T00:00:00Z',
    },
  });
  expect(itemResponse.ok()).toBeTruthy();
  const collection = await createCollectionViaAPI(request, {
    name: `Roadmap overdue collection ${stamp}`,
    workspace_id: workspace.id,
    ql_query: `title ~ "${stamp}"`,
  });
  const configuration = await request.post(
    `/api/collections/${collection.id}/board-configuration`,
    {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        columns: [],
        backlog_status_ids: [],
        list_columns: [],
        card_fields: [],
        roadmap_config: {
          start_field_id: 'due_date',
          end_field_id: '',
          dependency_link_type_id: null,
        },
        show_rightmost_column_last_50: false,
      },
    }
  );
  expect(configuration.ok()).toBeTruthy();

  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/roadmap`);
  await expect(page.getByTestId('roadmap-view')).toBeVisible();

  const settingsButton = page.getByTestId('roadmap-settings-button');
  await expect(settingsButton).toBeInViewport();
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth
      )
    )
    .toBe(true);
});

test('keeps a five-year quarter horizon and offers a return-to-today control', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Roadmap horizon ${stamp}`,
    key: `RH${stamp.toString().slice(-6)}`.toUpperCase(),
    description: 'Playwright roadmap horizon coverage',
  });
  await createItemViaAPI(request, workspace.id, {
    title: `Future planning item ${stamp}`,
  });
  const collection = await createCollectionViaAPI(request, {
    name: `Roadmap horizon collection ${stamp}`,
    workspace_id: workspace.id,
    ql_query: `title ~ "${stamp}"`,
  });
  const configuration = await request.post(
    `/api/collections/${collection.id}/board-configuration`,
    {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        columns: [],
        backlog_status_ids: [],
        list_columns: [],
        card_fields: [],
        roadmap_config: {
          start_field_id: 'start_date',
          end_field_id: 'end_date',
          dependency_link_type_id: null,
        },
        show_rightmost_column_last_50: false,
      },
    }
  );
  expect(configuration.ok()).toBeTruthy();

  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/roadmap`);
  await expect(page.getByTestId('roadmap-view')).toBeVisible();
  await page.getByTestId('roadmap-zoom-quarter').click();

  const timeline = page.getByTestId('roadmap-timeline-scroll');
  await expect(timeline).toBeVisible();
  await expect
    .poll(() => timeline.evaluate((element) => element.scrollWidth))
    .toBeGreaterThanOrEqual(60 * 80);

  const returnToToday = page.getByTestId('roadmap-return-today');
  await page.getByTestId('roadmap-scroll-right').click();
  await expect(returnToToday).toBeVisible();
  await returnToToday.click();
  await expect(returnToToday).toHaveCount(0);

  await timeline.evaluate((element) => {
    element.scrollLeft = element.scrollWidth;
  });
  await expect(returnToToday).toBeVisible();
  await returnToToday.click();
  await expect(returnToToday).toHaveCount(0);
  await expect.poll(() => timeline.evaluate((element) => element.scrollLeft)).toBe(0);
});

test('uses the workspace foreground token for the roadmap scheduling hint', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Roadmap gradient ${stamp}`,
    key: `RG${stamp.toString().slice(-6)}`.toUpperCase(),
    description: 'Playwright roadmap gradient contrast coverage',
  });
  await createItemViaAPI(request, workspace.id, {
    title: `Undated gradient item ${stamp}`,
  });
  const collection = await createCollectionViaAPI(request, {
    name: `Roadmap gradient collection ${stamp}`,
    workspace_id: workspace.id,
    ql_query: `title ~ "${stamp}"`,
  });
  const configurationResponse = await request.post(
    `/api/collections/${collection.id}/board-configuration`,
    {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        columns: [],
        backlog_status_ids: [],
        list_columns: [],
        card_fields: [],
        roadmap_config: {
          start_field_id: 'start_date',
          end_field_id: 'end_date',
          dependency_link_type_id: null,
        },
        show_rightmost_column_last_50: false,
      },
    }
  );
  expect(configurationResponse.ok()).toBeTruthy();
  const layoutResponse = await request.put(`/api/workspaces/${workspace.id}/homepage/layout`, {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
    data: {
      sections: [],
      widgets: [],
      gradient: 19,
      applyToAllViews: true,
      backgroundImageUrl: '',
    },
  });
  expect(layoutResponse.ok()).toBeTruthy();

  await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/roadmap`);
  const roadmap = page.getByTestId('roadmap-view');
  const hint = page.getByTestId('roadmap-unscheduled-hint');

  await expect(roadmap).toHaveCSS('background-image', /linear-gradient/);
  await expect(hint).toBeVisible();
  await expect(hint).toHaveCSS('color', 'rgb(255, 255, 255)');
});

test('previews and schedules an undated roadmap item with one click', async ({ page, request }) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Roadmap scheduling ${stamp}`,
    key: `RS${stamp.toString().slice(-6)}`.toUpperCase(),
    description: 'Playwright roadmap scheduling coverage',
  });
  const item = await createItemViaAPI(request, workspace.id, {
    title: `Unscheduled delivery ${stamp}`,
  });
  const partiallyScheduledResponse = await request.post('/api/items', {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
    data: {
      workspace_id: workspace.id,
      title: `Partially scheduled delivery ${stamp}`,
      start_date: new Date().toISOString(),
    },
  });
  expect(partiallyScheduledResponse.ok()).toBeTruthy();
  const partiallyScheduledItem = await partiallyScheduledResponse.json();
  const collection = await createCollectionViaAPI(request, {
    name: `Roadmap collection ${stamp}`,
    workspace_id: workspace.id,
    ql_query: `title ~ "${stamp}"`,
  });
  const configuration = await request.post(
    `/api/collections/${collection.id}/board-configuration`,
    {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        columns: [],
        backlog_status_ids: [],
        list_columns: [],
        card_fields: [],
        roadmap_config: {
          start_field_id: 'start_date',
          end_field_id: 'end_date',
          dependency_link_type_id: null,
        },
        show_rightmost_column_last_50: false,
      },
    }
  );
  expect(configuration.ok()).toBeTruthy();

  await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/roadmap`);
  await expect(page.getByTestId('roadmap-view')).toBeVisible();

  const scheduleRow = page.getByTestId(`roadmap-schedule-row-${item.id}`);
  await expect(scheduleRow).toBeVisible();
  await expect(page.getByTestId('roadmap-unscheduled-hint')).toContainText(/needs? dates/);
  await expect(page.getByTestId(`roadmap-schedule-row-${partiallyScheduledItem.id}`)).toHaveCount(
    0
  );
  await scheduleRow.hover({ position: { x: 120, y: 20 } });

  const preview = page.getByTestId(`roadmap-schedule-preview-${item.id}`);
  await expect(preview).toBeVisible();
  const startDate = await preview.getAttribute('data-start-date');
  const endDate = await preview.getAttribute('data-end-date');
  expect(startDate).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  expect(endDate).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  if (!startDate || !endDate) throw new Error('Roadmap preview dates are missing');
  expect(
    (Date.parse(`${endDate}T00:00:00Z`) - Date.parse(`${startDate}T00:00:00Z`)) /
      (24 * 60 * 60 * 1000)
  ).toBe(6);

  await scheduleRow.click({ position: { x: 120, y: 20 } });
  const scheduledBar = page.getByTestId(`roadmap-bar-${item.id}`);
  await expect(scheduledBar).toBeVisible();
  await expect(scheduledBar).toHaveAttribute('data-start-date', startDate);
  await expect(scheduledBar).toHaveAttribute('data-end-date', endDate);
  await scheduledBar.click();
  const itemPreview = page.getByTestId(`roadmap-item-preview-${item.id}`);
  await expect(itemPreview).toBeVisible();
  await expect(itemPreview).toContainText(item.title);

  await page.reload();
  await expect(page.getByTestId(`roadmap-bar-${item.id}`)).toHaveAttribute(
    'data-start-date',
    startDate
  );
  await expect(page.getByTestId(`roadmap-schedule-row-${item.id}`)).toHaveCount(0);
});

test('roll-down keeps inner child dates and persists only overflow after a parent shrink', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Roadmap roll-down ${stamp}`,
    key: `RD${stamp.toString().slice(-6)}`.toUpperCase(),
    description: 'Playwright roadmap hierarchy date coverage',
  });
  const parent = await createItemViaAPI(request, workspace.id, {
    title: `Hierarchy parent ${stamp}`,
    start_date: '2026-08-10T00:00:00Z',
    end_date: '2026-08-20T00:00:00Z',
  });
  const child = await createItemViaAPI(request, workspace.id, {
    title: `Hierarchy child ${stamp}`,
    parent_id: parent.id,
    start_date: '2026-08-12T00:00:00Z',
    end_date: '2026-08-25T00:00:00Z',
  });
  const collection = await createCollectionViaAPI(request, {
    name: `Roadmap hierarchy collection ${stamp}`,
    workspace_id: workspace.id,
    ql_query: `title ~ "${stamp}"`,
  });
  const configuration = await request.post(
    `/api/collections/${collection.id}/board-configuration`,
    {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        columns: [],
        backlog_status_ids: [],
        list_columns: [],
        card_fields: [],
        roadmap_config: {
          start_field_id: 'start_date',
          end_field_id: 'end_date',
          dependency_link_type_id: null,
        },
        show_rightmost_column_last_50: false,
      },
    }
  );
  expect(configuration.ok()).toBeTruthy();

  await page.goto(`/workspaces/${workspace.id}/collections/${collection.id}/roadmap`);
  await expect(page.getByTestId('roadmap-view')).toBeVisible();
  await page.getByTestId('roadmap-settings-button').click();
  await page.locator('#roadmap-start-field').click();
  await page.locator('#roadmap-start-field-option-start_date').click();
  await expect(page.getByTestId('roadmap-settings-panel')).toBeVisible();
  await page.getByTestId('roadmap-hierarchy-rollup').click();
  const parentBar = page.getByTestId(`roadmap-bar-${parent.id}`);
  await expect(parentBar).toHaveAttribute('data-start-date', '2026-08-12');
  await expect(parentBar).toHaveAttribute('data-end-date', '2026-08-25');
  await expect(parentBar).toHaveAttribute('data-hierarchy-summary', 'true');
  await expect(page.getByTestId(`roadmap-resize-right-${parent.id}`)).toHaveCount(0);
  const storedParent = await (await request.get(`/api/items/${parent.id}`)).json();
  expect(storedParent.start_date.slice(0, 10)).toBe('2026-08-10');
  expect(storedParent.end_date.slice(0, 10)).toBe('2026-08-20');

  await page.getByTestId('roadmap-hierarchy-rolldown').click();

  const childBar = page.getByTestId(`roadmap-bar-${child.id}`);
  await expect(childBar).toHaveAttribute('data-start-date', '2026-08-12');
  await expect(childBar).toHaveAttribute('data-end-date', '2026-08-20');
  let storedChild = await (await request.get(`/api/items/${child.id}`)).json();
  expect(storedChild.start_date.slice(0, 10)).toBe('2026-08-12');
  expect(storedChild.end_date.slice(0, 10)).toBe('2026-08-25');

  await page.getByTestId('roadmap-adjust-related-dates').click();
  storedChild = await (await request.get(`/api/items/${child.id}`)).json();
  expect(storedChild.end_date.slice(0, 10)).toBe('2026-08-25');

  const resizeHandle = page.getByTestId(`roadmap-resize-right-${parent.id}`);
  const handleBox = await resizeHandle.boundingBox();
  expect(handleBox).not.toBeNull();
  if (!handleBox) throw new Error('Roadmap resize handle has no bounding box');
  const bulkPatchResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/items/bulk-patch') && response.request().method() === 'POST'
  );
  await page.mouse.move(handleBox.x + handleBox.width / 2, handleBox.y + handleBox.height / 2);
  await page.mouse.down();
  await page.mouse.move(handleBox.x + handleBox.width / 2 - 18, handleBox.y + handleBox.height / 2);
  await page.mouse.up();
  expect((await bulkPatchResponse).ok()).toBeTruthy();

  await expect(parentBar).toHaveAttribute('data-end-date', '2026-08-18');
  await expect(childBar).toHaveAttribute('data-start-date', '2026-08-12');
  await expect(childBar).toHaveAttribute('data-end-date', '2026-08-18');
  storedChild = await (await request.get(`/api/items/${child.id}`)).json();
  expect(storedChild.start_date.slice(0, 10)).toBe('2026-08-12');
  expect(storedChild.end_date.slice(0, 10)).toBe('2026-08-18');
});
