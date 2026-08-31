import type { BrowserContext } from '@playwright/test';
import { createTeamViaAPI, createUserViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateTeam, generateUser } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function loginAs(context: BrowserContext, username: string, password: string): Promise<void> {
  const response = await context.request.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: {
      email_or_username: username,
      password,
      remember_me: false,
    },
  });
  expect(
    response.ok(),
    `login as ${username} failed (${response.status()}): ${await response.text()}`
  ).toBeTruthy();
}

test.describe('On-call roster visibility', () => {
  test('does not expose roster identities to a non-member', async ({ browser, request }) => {
    const team = await createTeamViaAPI(request, generateTeam('roster-visibility'));
    const rosterUserData = generateUser('roster-member');
    const rosterUser = await createUserViaAPI(request, rosterUserData);
    const outsiderUserData = generateUser('roster-outsider');
    await createUserViaAPI(request, outsiderUserData);

    const memberResponse = await request.post(`/api/teams/${team.id}/members`, {
      headers: SEC_FETCH,
      data: { user_ids: [rosterUser.id], role: 'member' },
    });
    expect(
      memberResponse.ok(),
      `add roster member failed (${memberResponse.status()}): ${await memberResponse.text()}`
    ).toBeTruthy();

    const scheduleResponse = await request.post(`/api/teams/${team.id}/on-call/schedules`, {
      headers: SEC_FETCH,
      data: {
        name: 'Primary schedule',
        description: 'Visibility regression fixture',
        timezone: 'UTC',
      },
    });
    expect(scheduleResponse.ok()).toBeTruthy();
    const schedule = (await scheduleResponse.json()) as { id: number };

    const layerResponse = await request.post(`/api/on-call/schedules/${schedule.id}/layers`, {
      headers: SEC_FETCH,
      data: {
        name: 'Primary rotation',
        priority: 1,
        rotation_type: 'weekly',
        rotation_interval_days: 7,
        handoff_time: '09:00',
        start_date: new Date().toISOString().slice(0, 10),
        end_date: null,
      },
    });
    expect(layerResponse.ok()).toBeTruthy();
    const layer = (await layerResponse.json()) as { id: number };

    const layerMembersResponse = await request.put(
      `/api/on-call/schedules/${schedule.id}/layers/${layer.id}/members`,
      {
        headers: SEC_FETCH,
        data: { user_ids: [rosterUser.id] },
      }
    );
    expect(layerMembersResponse.ok()).toBeTruthy();

    const outsiderContext = await browser.newContext({
      storageState: { cookies: [], origins: [] },
    });
    try {
      await loginAs(outsiderContext, outsiderUserData.username, outsiderUserData.password_hash);
      const page = await outsiderContext.newPage();
      await page.goto(`/teams/${team.id}/on-call`);
      await expect(page.getByTestId('on-call-tab')).toBeVisible();

      // The fixture creates exactly one schedule, so the feature-scoped test
      // id is sufficient and remains within the stable-selector policy.
      const scheduleRow = page.getByTestId('schedule-row').first();
      await expect(scheduleRow).toBeVisible();
      const layerRow = scheduleRow.getByTestId('layer-row');
      await expect(layerRow).toHaveCount(1);

      // This is the intended contract: the schedule directory may be visible,
      // but roster identities must remain restricted to team members/admins.
      await expect(layerRow).not.toContainText(rosterUserData.last_name);
    } finally {
      await outsiderContext.close();
    }
  });
});
