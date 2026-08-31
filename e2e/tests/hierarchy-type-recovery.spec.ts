import type { APIRequestContext } from '@playwright/test';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

type ItemType = {
  id: number;
  name: string;
  description: string;
  is_default: boolean;
  icon: string;
  color: string;
  hierarchy_level: number;
  sort_order: number;
  configuration_set_ids?: number[];
};

type WorkItem = {
  id: number;
  title: string;
};

async function createItemType(
  request: APIRequestContext,
  name: string,
  hierarchyLevel: number,
  sortOrder: number,
  color: string
): Promise<ItemType> {
  const response = await request.post('/api/item-types', {
    headers: SEC_FETCH,
    data: {
      name,
      description: `E2E hierarchy recovery type at level ${hierarchyLevel}`,
      is_default: false,
      icon: 'Circle',
      color,
      hierarchy_level: hierarchyLevel,
      sort_order: sortOrder,
    },
  });
  expect(
    response.ok(),
    `create item type ${name}: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
  return response.json();
}

async function createItem(
  request: APIRequestContext,
  workspaceId: number,
  title: string,
  itemTypeId: number,
  parentId?: number
): Promise<WorkItem> {
  const response = await request.post('/api/items', {
    headers: SEC_FETCH,
    data: {
      workspace_id: workspaceId,
      title,
      item_type_id: itemTypeId,
      ...(parentId !== undefined ? { parent_id: parentId } : {}),
    },
  });
  expect(
    response.ok(),
    `create item ${title}: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
  return response.json();
}

test('a compatible item type restores reparenting after hierarchy configuration drift', async ({
  page,
  request,
}) => {
  test.setTimeout(90_000);
  await page.setViewportSize({ width: 1440, height: 1000 });

  const suffix = Date.now().toString(36);
  const parentType = await createItemType(request, `RecoveryParent-${suffix}`, 2, 90, '#2563eb');
  const movableType = await createItemType(request, `RecoveryTask-${suffix}`, 3, 91, '#dc2626');
  const compatibleType = await createItemType(request, `RecoveryBug-${suffix}`, 3, 92, '#ea580c');
  const childType = await createItemType(request, `RecoverySubtask-${suffix}`, 4, 93, '#6b7280');

  const workspace = await createWorkspaceViaAPI(request, {
    name: `Hierarchy Recovery ${suffix}`,
    key: `HR${suffix.slice(-6).toUpperCase()}`,
    description: 'Isolated workspace for hierarchy recovery UX',
  });

  const originalParent = await createItem(
    request,
    workspace.id,
    `Original parent ${suffix}`,
    parentType.id
  );
  const replacementParent = await createItem(
    request,
    workspace.id,
    `Replacement parent ${suffix}`,
    parentType.id
  );
  const legacyItem = await createItem(
    request,
    workspace.id,
    `Legacy task ${suffix}`,
    movableType.id,
    originalParent.id
  );
  const child = await createItem(
    request,
    workspace.id,
    `Legacy subtask ${suffix}`,
    childType.id,
    legacyItem.id
  );

  // Exercise the real admin UI: moving the type from level 3 to level 2
  // creates configuration drift without migrating the stored parent links.
  await page.goto('/admin/item-types');
  const movableTypeRow = page.getByTestId(`item-type-row-${movableType.id}`);
  await expect(movableTypeRow).toBeVisible();
  await page.getByTestId(`item-type-actions-${movableType.id}`).click();
  await page.getByTestId('item-type-edit').click();
  await page.locator('#hierarchy_level').click();
  await page.locator('#hierarchy_level-option-2').click();
  await expect(page.getByTestId('admin-hierarchy-change-warning')).toBeVisible();
  await page.getByTestId('dialog-confirm').click();
  await expect(movableTypeRow).toHaveAttribute('data-hierarchy-level', '2');

  // Existing parent and child links remain visible even though the configured
  // levels are now L2 -> L2 -> L4.
  await page.goto(`/workspaces/${workspace.id}/items/${legacyItem.id}`);
  await expect(page.getByTestId('item-detail-ready')).toBeVisible();
  await expect(page.getByTestId(`item-parent-breadcrumb-${originalParent.id}`)).toBeVisible();
  await expect(page.getByTestId(`item-child-card-${child.id}`)).toBeVisible();
  await expect(page.getByTestId('item-type-change-trigger')).toHaveAttribute(
    'data-item-type-id',
    String(movableType.id)
  );
  const itemTypeTriggerBox = await page.getByTestId('item-type-change-trigger').boundingBox();
  const itemTypeIconBox = await page.getByTestId('item-type-change-icon').boundingBox();
  expect(itemTypeTriggerBox).not.toBeNull();
  expect(itemTypeIconBox).not.toBeNull();
  if (itemTypeTriggerBox && itemTypeIconBox) {
    const triggerCenter = itemTypeTriggerBox.y + itemTypeTriggerBox.height / 2;
    const iconCenter = itemTypeIconBox.y + itemTypeIconBox.height / 2;
    expect(Math.abs(triggerCenter - iconCenter)).toBeLessThanOrEqual(1);
  }
  const parentBreadcrumbBox = await page
    .getByTestId(`item-parent-breadcrumb-${originalParent.id}`)
    .boundingBox();
  const parentItemTypeIconBox = await page
    .getByTestId(`item-parent-type-icon-${originalParent.id}`)
    .boundingBox();
  const parentItemTypeIconSlotBox = await page
    .getByTestId(`item-parent-type-icon-slot-${originalParent.id}`)
    .boundingBox();
  expect(parentBreadcrumbBox).not.toBeNull();
  expect(parentItemTypeIconBox).not.toBeNull();
  expect(parentItemTypeIconSlotBox).not.toBeNull();
  if (parentBreadcrumbBox && parentItemTypeIconBox) {
    const breadcrumbCenter = parentBreadcrumbBox.y + parentBreadcrumbBox.height / 2;
    const iconCenter = parentItemTypeIconBox.y + parentItemTypeIconBox.height / 2;
    expect(Math.abs(breadcrumbCenter - iconCenter)).toBeLessThanOrEqual(1);
  }
  if (parentItemTypeIconSlotBox && itemTypeTriggerBox) {
    expect(parentItemTypeIconSlotBox.width).toBe(itemTypeTriggerBox.width);
    expect(parentItemTypeIconSlotBox.height).toBe(itemTypeTriggerBox.height);
  }
  await expect(page.getByTestId('item-hierarchy-mismatch')).toHaveAttribute(
    'data-current-level',
    '2'
  );
  await expect(page.getByTestId('item-hierarchy-mismatch')).toHaveAttribute(
    'data-parent-level',
    '2'
  );

  // The reparent picker now treats the legacy item as level 2, so it searches
  // only for level 1 parents and hides the level 2 replacement.
  await page.getByTestId('item-detail-breadcrumbs').hover();
  await page.getByTestId('item-parent-edit').click();
  await expect(page.getByTestId('item-parent-selector')).toBeVisible();
  await expect(page.getByTestId('item-parent-level-hint')).toHaveAttribute(
    'data-parent-level',
    '1'
  );
  await page.getByTestId('item-parent-search').fill(replacementParent.title);
  await expect(page.getByTestId('item-parent-no-results')).toBeVisible();
  await expect(page.getByTestId('item-parent-hierarchy-mismatch')).toBeVisible();

  // The type picker offers the compatible level 3 type. Selecting it repairs
  // both sides of the legacy L2 parent -> item -> L4 child relationship.
  await page.getByTestId('item-parent-change-type').click();
  await expect(page.getByTestId('item-type-selector')).toBeVisible();
  await expect(page.getByTestId('item-type-compatibility-hint')).toHaveAttribute(
    'data-compatible-level',
    '3'
  );
  await expect(page.getByTestId(`item-type-option-${compatibleType.id}`)).toHaveAttribute(
    'data-parent-compatible',
    'true'
  );
  await expect(page.getByTestId(`item-type-option-${movableType.id}`)).toHaveAttribute(
    'data-parent-compatible',
    'false'
  );
  await expect(page.getByTestId(`item-type-compatible-${compatibleType.id}`)).toBeVisible();
  await page.getByTestId(`item-type-option-${compatibleType.id}`).click();
  await expect(page.getByTestId('item-type-change-trigger')).toHaveAttribute(
    'data-item-type-id',
    String(compatibleType.id)
  );
  await expect(page.getByTestId(`item-parent-breadcrumb-${originalParent.id}`)).toBeVisible();
  await expect(page.getByTestId(`item-child-card-${child.id}`)).toBeVisible();
  await expect(page.getByTestId('item-hierarchy-mismatch')).toHaveCount(0);

  // With the item back at level 3, the same picker now exposes level 2 parents
  // and the reparent completes through the browser-visible flow.
  await page.getByTestId('item-detail-breadcrumbs').hover();
  await page.getByTestId('item-parent-edit').click();
  await expect(page.getByTestId('item-parent-level-hint')).toHaveAttribute(
    'data-parent-level',
    '2'
  );
  await page.getByTestId('item-parent-search').fill(replacementParent.title);
  await expect(page.getByTestId(`item-parent-result-${replacementParent.id}`)).toBeVisible();
  await page.getByTestId(`item-parent-result-${replacementParent.id}`).click();

  await expect(page.getByTestId(`item-parent-breadcrumb-${replacementParent.id}`)).toBeVisible();
  await expect(page.getByTestId(`item-parent-breadcrumb-${originalParent.id}`)).toHaveCount(0);
  await expect(page.getByTestId(`item-child-card-${child.id}`)).toBeVisible();
});
