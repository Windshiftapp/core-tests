import { randomUUID } from 'node:crypto';
import type { APIRequestContext } from '@playwright/test';
import {
  createCollectionViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
  listItemTypesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { BoardPage } from '../pages/board.page';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

type ItemType = {
  id: number;
  name: string;
  hierarchy_level?: number;
};

async function createRestrictedWorkspace(request: APIRequestContext) {
  const suffix = `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
  const workspace = await createWorkspaceViaAPI(request, {
    name: `Board type scope ${suffix}`,
    key: `BT${randomUUID().replaceAll('-', '').slice(0, 8).toUpperCase()}`,
    description: 'Board quick-add item-type scoping E2E',
  });
  const regularTypes = (await listItemTypesViaAPI(request)).filter(
    (type: ItemType) => (type.hierarchy_level ?? 0) !== -1
  );
  expect(regularTypes.length, 'at least two regular item types').toBeGreaterThan(1);
  const allowedType = regularTypes[0];
  const excludedType = regularTypes[1];

  const configResponse = await request.post('/api/configuration-sets', {
    headers: SEC_FETCH,
    data: {
      name: `Board type scope ${suffix}`,
      description: 'Allow one item type in the board quick-add workspace',
      workspace_ids: [workspace.id],
      item_type_configs: [{ item_type_id: allowedType.id }],
      default_item_type_id: allowedType.id,
    },
  });
  expect(
    configResponse.ok(),
    `create configuration set: ${configResponse.status()} ${await configResponse.text()}`
  ).toBeTruthy();

  return { workspace, allowedType, excludedType, suffix };
}

async function openFirstQuickAdd(page: import('@playwright/test').Page) {
  await page.getByTestId('board-view').waitFor();
  await page
    .getByTestId(/^board-column-add-/)
    .first()
    .click();
}

async function expectOnlyAllowedType(
  page: import('@playwright/test').Page,
  allowedType: ItemType,
  excludedType: ItemType
) {
  await page.getByTestId('quick-add-type').click();
  const options = page.getByTestId(/^quick-add-type-option-/);
  await expect(options).toHaveCount(1);
  await expect(page.getByTestId(`quick-add-type-option-${allowedType.id}`)).toBeVisible();
  await expect(page.getByTestId(`quick-add-type-option-${excludedType.id}`)).toHaveCount(0);
}

test.describe('Board quick-add workspace item types', () => {
  test('limits the default collection to its workspace configuration', async ({
    page,
    request,
  }) => {
    const { workspace, allowedType, excludedType } = await createRestrictedWorkspace(request);
    const board = new BoardPage(page);
    await board.goto(String(workspace.id));
    await board.verifyBoardVisible();

    await openFirstQuickAdd(page);
    await page.getByTestId('quick-add-workspace').click();
    await expect(page.getByTestId(/^quick-add-workspace-option-/)).toHaveCount(1);
    await expect(page.getByTestId(`quick-add-workspace-option-${workspace.id}`)).toBeVisible();
    await page.getByTestId(`quick-add-workspace-option-${workspace.id}`).click();
    await expectOnlyAllowedType(page, allowedType, excludedType);
  });

  test('limits a global collection to its referenced workspaces and re-scopes item types', async ({
    page,
    request,
  }) => {
    const { workspace, allowedType, excludedType, suffix } =
      await createRestrictedWorkspace(request);
    const secondWorkspace = await createWorkspaceViaAPI(request, {
      name: `Second board scope ${suffix}`,
      key: `BS${randomUUID().replaceAll('-', '').slice(0, 8).toUpperCase()}`,
      description: 'Second referenced workspace for board quick add',
    });
    const marker = `board-scope-${randomUUID()}`;
    const restrictedSeedResponse = await request.post('/api/items', {
      headers: SEC_FETCH,
      data: {
        workspace_id: workspace.id,
        item_type_id: allowedType.id,
        title: `${marker} restricted`,
      },
    });
    expect(restrictedSeedResponse.ok()).toBeTruthy();
    await createItemViaAPI(request, secondWorkspace.id, {
      title: `${marker} second`,
    });
    const collection = await createCollectionViaAPI(request, {
      name: `Cross-workspace board ${suffix}`,
      description: 'Global collection for board quick-add item-type scoping',
      workspace_id: null,
      ql_query: `title ~ "${marker}"`,
    });

    await page.goto(`/collections/${collection.id}/board`);
    await openFirstQuickAdd(page);
    await page.getByTestId('quick-add-workspace').click();
    const workspaceOptions = page.getByTestId(/^quick-add-workspace-option-/);
    await expect(workspaceOptions).toHaveCount(2);
    await expect(
      page.getByTestId(`quick-add-workspace-option-${secondWorkspace.id}`)
    ).toBeVisible();
    await page.getByTestId(`quick-add-workspace-option-${workspace.id}`).click();

    await expectOnlyAllowedType(page, allowedType, excludedType);
  });
});
