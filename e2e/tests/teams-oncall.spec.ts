import { createUserViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateSchedule, generateTeam, generateUser } from '../fixtures/test-data';
import { TeamOnCallPage } from '../pages/team-oncall.page';
import { TeamsPage } from '../pages/teams.page';

/**
 * On-call schedule flows for the Teams feature.
 *
 * Backend status: incident creation + escalation policy dispatch are NOT
 * wired into notification_service yet (see plan / project memory). These
 * tests exercise the configuration UX only — schedules, layers, and
 * overrides — which are fully wired end-to-end.
 */

test.describe('Team On-Call Schedules', () => {
  let teamsPage: TeamsPage;
  let oncall: TeamOnCallPage;
  let team: ReturnType<typeof generateTeam>;
  let teamId: number;

  test.beforeEach(async ({ page }) => {
    teamsPage = new TeamsPage(page);
    oncall = new TeamOnCallPage(page);
    team = generateTeam();
    await teamsPage.createTeam(team);
    await teamsPage.goto();
    await teamsPage.openTeam(team.name);
    teamId = await teamsPage.getTeamIdFromUrl();
    await oncall.goto(teamId);
  });

  test('creates an on-call schedule', async () => {
    const schedule = generateSchedule();
    await oncall.createSchedule(schedule);
    await oncall.expectScheduleVisible(schedule.name);
  });

  test('adds a weekly rotation layer with two members and reorders them', async ({ request }) => {
    const schedule = generateSchedule();
    await oncall.createSchedule(schedule);

    // Keep suffixes short: the username column is capped at 32 chars and
    // generateUser already appends its own random nonce for uniqueness.
    const u1 = generateUser('ocl1');
    const u2 = generateUser('ocl2');
    const created1 = await createUserViaAPI(request, {
      email: u1.email,
      username: u1.username,
      first_name: u1.first_name,
      last_name: u1.last_name,
      password_hash: u1.password_hash,
    });
    const created2 = await createUserViaAPI(request, {
      email: u2.email,
      username: u2.username,
      first_name: u2.first_name,
      last_name: u2.last_name,
      password_hash: u2.password_hash,
    });

    await oncall.openAddLayerForm(schedule.name);
    await oncall.fillLayerForm({
      name: 'Primary',
      intervalDays: 7,
      handoffTime: '09:00',
      startDate: new Date().toISOString().slice(0, 10),
    });
    await oncall.addLayerMember(created1.id, u1.username);
    await oncall.addLayerMember(created2.id, u2.username);

    // Move u1 down so order becomes [u2, u1]
    await oncall.moveMemberDown(0);

    await oncall.saveLayer();
    await oncall.expectLayerVisible(schedule.name, 'Primary');

    // After save, pills are rendered as `${position + 1}. ${user_name}` where
    // user_name is "first_name last_name" — not username. Pills sort by
    // position ascending, so the lowest-position pill is the first one.
    const layer = oncall.layerRow(schedule.name).first();
    const memberPills = layer.locator('span').filter({ hasText: /^\d+\.\s/ });
    await expect(memberPills.first()).toContainText(u2.last_name);
  });

  test('creates an override and the row appears', async ({ request }) => {
    const schedule = generateSchedule();
    await oncall.createSchedule(schedule);

    // Short suffixes — username is capped at 32 chars (generateUser adds its
    // own nonce, so these stay unique).
    const replaced = generateUser('ovrA');
    const replacement = generateUser('ovrB');
    const createdReplaced = await createUserViaAPI(request, {
      email: replaced.email,
      username: replaced.username,
      first_name: replaced.first_name,
      last_name: replaced.last_name,
      password_hash: replaced.password_hash,
    });
    const createdReplacement = await createUserViaAPI(request, {
      email: replacement.email,
      username: replacement.username,
      first_name: replacement.first_name,
      last_name: replacement.last_name,
      password_hash: replacement.password_hash,
    });

    const tomorrow = new Date(Date.now() + 24 * 60 * 60 * 1000);
    const dayAfter = new Date(Date.now() + 48 * 60 * 60 * 1000);
    const fmt = (d: Date) =>
      `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}T${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;

    await oncall.createOverride(schedule.name, {
      replacedUserId: createdReplaced.id,
      replacedSearchTerm: replaced.username,
      replacementUserId: createdReplacement.id,
      replacementSearchTerm: replacement.username,
      startTime: fmt(tomorrow),
      endTime: fmt(dayAfter),
    });

    // The override (current + upcoming) must render in the schedule's Overrides
    // list, showing who is replaced, who covers, and the window. Users surface
    // as "first last" full names, not usernames.
    const row = oncall.overrideRow(schedule.name).first();
    await expect(row).toBeVisible({ timeout: 10000 });
    await expect(row.locator('[data-testid="override-replaced"]')).toContainText(
      replaced.last_name
    );
    await expect(row.locator('[data-testid="override-replacement"]')).toContainText(
      replacement.last_name
    );
    await expect(row.locator('[data-testid="override-window"]')).not.toBeEmpty();
  });

  test('deep-link to /teams/:id/on-call works', async ({ page }) => {
    await page.goto(`/teams/${teamId}/on-call`);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('[data-testid="team-detail"]')).toBeVisible();
  });
});
