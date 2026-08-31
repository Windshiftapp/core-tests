import {
  createPriorityViaAPI,
  deletePriorityViaAPI,
  listPrioritiesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generatePriority } from '../fixtures/test-data';
import { PriorityManagerPage } from '../pages/priority-manager.page';

/**
 * Priority Manager E2E Tests
 * Tests CRUD operations for priorities via the admin UI.
 * The "create" test is the most important — it covers a regression where adding priorities didn't work.
 */

test.describe('Priority Manager', () => {
  let priorityManagerPage: PriorityManagerPage;
  const createdPriorityNames: string[] = [];

  test.beforeEach(async ({ page }) => {
    priorityManagerPage = new PriorityManagerPage(page);
  });

  test.afterAll(async ({ request }) => {
    // Clean up all priorities created during this test run
    const priorities = await listPrioritiesViaAPI(request);
    for (const name of createdPriorityNames) {
      const priority = priorities.find((p: { name: string }) => p.name === name);
      if (priority) {
        await deletePriorityViaAPI(request, priority.id);
      }
    }
  });

  test('should create a priority', async () => {
    const data = generatePriority();
    createdPriorityNames.push(data.name);

    await priorityManagerPage.goto();
    await priorityManagerPage.createPriority({
      name: data.name,
      description: data.description,
      sortOrder: data.sort_order,
    });
    await priorityManagerPage.verifyPriorityExists(data.name);
  });

  test('should edit a priority', async ({ request }) => {
    // Create a priority via API for setup
    const data = generatePriority('edit-test');
    createdPriorityNames.push(data.name);
    await createPriorityViaAPI(request, data);

    const newName = `E2E Priority Edited ${Date.now()}`;
    createdPriorityNames.push(newName);

    await priorityManagerPage.goto();
    await priorityManagerPage.verifyPriorityExists(data.name);

    await priorityManagerPage.editPriority(data.name, {
      name: newName,
      sortOrder: 50,
    });

    await priorityManagerPage.verifyPriorityExists(newName);
    await priorityManagerPage.verifyPriorityDoesNotExist(data.name);
  });

  test('should delete a priority', async () => {
    const data = generatePriority('delete-test');
    createdPriorityNames.push(data.name);

    await priorityManagerPage.goto();
    await priorityManagerPage.createPriority({
      name: data.name,
      description: data.description,
    });
    await priorityManagerPage.verifyPriorityExists(data.name);

    await priorityManagerPage.deletePriority(data.name);
    await priorityManagerPage.verifyPriorityDoesNotExist(data.name);
  });

  test('should show validation error for empty name', async ({ page }) => {
    await priorityManagerPage.goto();
    await priorityManagerPage.clickCreatePriority();

    // Clear the name field and try to save
    await priorityManagerPage.fillName('');
    await priorityManagerPage.clickSave();

    // Verify error message appears
    const errorMessage = await priorityManagerPage.getErrorMessage();
    expect(errorMessage).toContain('Priority name is required');

    // Verify modal stays open
    await expect(page.locator(priorityManagerPage.modal)).toBeVisible();
  });
});
