import { createGroupViaAPI, createUserViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateGroup, generateTeam, generateUser } from '../fixtures/test-data';
import { TeamsPage } from '../pages/teams.page';

/**
 * Teams admin tests — composition CRUD + members + groups against /teams.
 *
 * The Teams feature is the cross-workspace org with on-call rotations.
 * On-call schedule flows live in teams-oncall.spec.ts.
 */

test.describe('Team Management', () => {
  let teamsPage: TeamsPage;
  let team: ReturnType<typeof generateTeam>;

  test.beforeEach(async ({ page }) => {
    teamsPage = new TeamsPage(page);
    team = generateTeam();
  });

  test.describe('Create team', () => {
    test('creates a team with name and description', async () => {
      await teamsPage.createTeam(team);
      await teamsPage.verifyTeamExists(team.name);
    });

    test('creates a team with name only', async () => {
      const onlyName = { name: team.name };
      await teamsPage.createTeam(onlyName);
      await teamsPage.verifyTeamExists(team.name);
    });
  });

  test.describe('Edit team', () => {
    test.beforeEach(async () => {
      await teamsPage.createTeam(team);
    });

    test('renames a team', async () => {
      const newName = `${team.name} – Renamed`;
      await teamsPage.editTeam(team.name, { name: newName });
      await teamsPage.verifyTeamExists(newName);
    });

    test('updates description', async () => {
      const newDesc = 'Updated team description';
      await teamsPage.editTeam(team.name, { description: newDesc });
      await teamsPage.goto();
      await expect(teamsPage.findTeamRow(team.name)).toContainText(newDesc);
    });
  });

  test.describe('Delete team', () => {
    test.beforeEach(async () => {
      await teamsPage.createTeam(team);
    });

    test('deletes a team', async () => {
      await teamsPage.deleteTeam(team.name);
      await teamsPage.verifyTeamDoesNotExist(team.name);
    });
  });

  test.describe('Members', () => {
    test.beforeEach(async () => {
      await teamsPage.createTeam(team);
    });

    test('adds a direct member', async ({ request }) => {
      // Short suffix: username is capped at 32 chars (generateUser appends its
      // own nonce for uniqueness).
      const user = generateUser('tm');
      const created = await createUserViaAPI(request, {
        email: user.email,
        username: user.username,
        first_name: user.first_name,
        last_name: user.last_name,
        password_hash: user.password_hash,
      });

      await teamsPage.addMember(team.name, created.id, user.username);
      await expect(
        teamsPage.page.locator(`[data-testid="member-row"][data-user-id="${created.id}"]`)
      ).toBeVisible();
    });
  });

  test.describe('Groups', () => {
    test.beforeEach(async () => {
      await teamsPage.createTeam(team);
    });

    test('attaches a group', async ({ request }) => {
      const group = generateGroup();
      const created = await createGroupViaAPI(request, group);

      await teamsPage.attachGroup(team.name, created.id);

      // The attached row must be the group we picked — identified by its id and
      // showing its name — not merely "some row exists".
      const row = teamsPage.page.locator(
        `[data-testid="group-row"][data-group-id="${created.id}"]`
      );
      await expect(row).toBeVisible();
      await expect(row).toContainText(group.name);
    });
  });

  test.describe('List', () => {
    test('lists multiple teams', async () => {
      const t1 = generateTeam('list-1');
      const t2 = generateTeam('list-2');
      const t3 = generateTeam('list-3');
      await teamsPage.createTeam(t1);
      await teamsPage.createTeam(t2);
      await teamsPage.createTeam(t3);

      await teamsPage.goto();
      await teamsPage.verifyTeamExists(t1.name);
      await teamsPage.verifyTeamExists(t2.name);
      await teamsPage.verifyTeamExists(t3.name);
      const count = await teamsPage.getTeamCount();
      expect(count).toBeGreaterThanOrEqual(3);
    });
  });
});
