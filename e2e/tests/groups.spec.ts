import { createGroupViaAPI, createUserViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateGroup, generateUser } from '../fixtures/test-data';
import { GroupPage } from '../pages/group.page';

/**
 * Groups admin tests — CRUD + member management against /admin/groups.
 *
 * (The spec used to be called "Team Management"; "Teams" in this file
 * means /admin/groups, not the FE-unwired /api/teams backend.)
 */

test.describe('Group Management', () => {
  let groupPage: GroupPage;
  let testGroup: ReturnType<typeof generateGroup>;

  test.beforeEach(async ({ page }) => {
    groupPage = new GroupPage(page);
    testGroup = generateGroup();
  });

  test.describe('Create Group', () => {
    test('should create group with valid data', async () => {
      await groupPage.createGroup(testGroup);
      await groupPage.verifyGroupExists(testGroup.name);
    });

    test('should create group with name only', async () => {
      await groupPage.createGroup({ name: testGroup.name });
      await groupPage.verifyGroupExists(testGroup.name);
    });

    test('should display group in list after creation', async () => {
      await groupPage.createGroup(testGroup);
      await groupPage.goto();

      const group = await groupPage.findGroupByName(testGroup.name);
      await expect(group).toBeVisible();
      await expect(group).toContainText(testGroup.name);
    });
  });

  test.describe('Edit Group', () => {
    test.beforeEach(async ({ request }) => {
      await createGroupViaAPI(request, testGroup);
    });

    test('should update group name', async () => {
      const newName = `${testGroup.name} - Updated`;
      await groupPage.editGroup(testGroup.name, { name: newName });
      await groupPage.verifyGroupExists(newName);
    });

    test('should update group description', async () => {
      const newDescription = 'Updated group description';
      await groupPage.editGroup(testGroup.name, { description: newDescription });

      await groupPage.goto();
      const group = await groupPage.findGroupByName(testGroup.name);
      await expect(group).toContainText(newDescription);
    });
  });

  test.describe('Delete Group', () => {
    test.beforeEach(async ({ request }) => {
      await createGroupViaAPI(request, testGroup);
    });

    test('should delete group', async () => {
      await groupPage.deleteGroup(testGroup.name);
      await groupPage.verifyGroupDoesNotExist(testGroup.name);
    });
  });

  test.describe('Group Members', () => {
    test.beforeEach(async ({ request }) => {
      await createGroupViaAPI(request, testGroup);
    });

    test('should add member to group', async ({ request }) => {
      // User creation is a two-step flow (create then activate) since core
      // commit 73b2b39 — createUserViaAPI handles the activate POST itself.
      const testUser = generateUser('group-member');
      await createUserViaAPI(request, {
        email: testUser.email,
        username: testUser.username,
        first_name: testUser.first_name,
        last_name: testUser.last_name,
        password_hash: testUser.password_hash,
      });

      // Drive the picker by email — its <option> nodes render first_name +
      // last_name + email (no username), and the picker's client-side filter
      // matches email substring. The Members list row also shows the email,
      // so the same value works for the verify step.
      await groupPage.addMember(testGroup.name, testUser.email);
      await groupPage.verifyMemberInGroup(testGroup.name, testUser.email);
    });

    test('should remove member from group', async ({ request }) => {
      // User creation is a two-step flow (create then activate) since core
      // commit 73b2b39 — createUserViaAPI handles the activate POST itself.
      const testUser = generateUser('group-remove');
      await createUserViaAPI(request, {
        email: testUser.email,
        username: testUser.username,
        first_name: testUser.first_name,
        last_name: testUser.last_name,
        password_hash: testUser.password_hash,
      });

      // Drive the picker by email — its <option> nodes render first_name +
      // last_name + email (no username), and the picker's client-side filter
      // matches email substring. The Members list row also shows the email,
      // so the same value works for the verify step.
      await groupPage.addMember(testGroup.name, testUser.email);
      await groupPage.verifyMemberInGroup(testGroup.name, testUser.email);

      await groupPage.removeMember(testGroup.name, testUser.email);

      // Verify member was removed — reopen members modal
      await groupPage.openMembers(testGroup.name);
      const memberText = groupPage.page
        .locator('div[role="dialog"]')
        .locator(`text=${testUser.email}`);
      await expect(memberText).not.toBeVisible({ timeout: 5000 });
    });
  });

  test.describe('Group List', () => {
    test('should display multiple groups', async () => {
      const group1 = generateGroup('1');
      const group2 = generateGroup('2');
      const group3 = generateGroup('3');

      await groupPage.createGroup(group1);
      await groupPage.createGroup(group2);
      await groupPage.createGroup(group3);

      await groupPage.goto();

      await groupPage.verifyGroupExists(group1.name);
      await groupPage.verifyGroupExists(group2.name);
      await groupPage.verifyGroupExists(group3.name);

      const count = await groupPage.getGroupCount();
      expect(count).toBeGreaterThanOrEqual(3);
    });
  });
});
