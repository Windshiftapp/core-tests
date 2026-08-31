import { expect, type Page } from '../fixtures/context-path';
import { pickUser } from './teams.page';

/**
 * Page Object for the on-call section of a team detail page at
 * /teams/:id/on-call. Drives the schedule, layer, and override editors.
 */
export class TeamOnCallPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  async goto(teamId: number) {
    await this.page.goto(`/teams/${teamId}/on-call`);
    await this.page.waitForLoadState('networkidle');
  }

  async createSchedule(data: { name: string; description?: string; timezone?: string }) {
    await this.page.locator('[data-testid="add-schedule"]').click();
    await this.page.waitForSelector('div[role="dialog"]', { timeout: 5000 });
    await this.page.fill('#schedule-name', data.name);
    if (data.description !== undefined) {
      await this.page.fill('#schedule-description', data.description);
    }
    // schedule-timezone is a custom Select — only set if explicitly given. Otherwise
    // accept the auto-detected default.
    await this.page.locator('[data-testid="schedule-save"]').click();
    await this.page.locator('div[role="dialog"]').waitFor({ state: 'detached', timeout: 10000 });
  }

  scheduleRow(name: string) {
    return this.page.locator('[data-testid="schedule-row"]', { hasText: name }).first();
  }

  async openAddLayerForm(scheduleName: string) {
    const row = this.scheduleRow(scheduleName);
    await row.locator('[data-testid="layer-add"]').click();
    await this.page.waitForSelector('div[role="dialog"]', { timeout: 5000 });
  }

  async fillLayerForm(data: {
    name: string;
    rotationType?: 'daily' | 'weekly' | 'custom';
    intervalDays?: number;
    handoffTime?: string;
    startDate?: string;
  }) {
    await this.page.fill('#layer-name', data.name);
    if (data.intervalDays !== undefined) {
      await this.page.fill('#layer-interval', String(data.intervalDays));
    }
    if (data.handoffTime) {
      await this.page.fill('#layer-handoff', data.handoffTime);
    }
    if (data.startDate) {
      await this.page.fill('#layer-start', data.startDate);
    }
  }

  async addLayerMember(userId: number, searchTerm: string) {
    const dialog = this.page.locator('div[role="dialog"]');
    await pickUser(
      this.page,
      dialog.locator('[data-testid="user-picker-trigger"]'),
      userId,
      searchTerm
    );
  }

  async saveLayer() {
    await this.page.locator('[data-testid="layer-save"]').click();
    await this.page.locator('div[role="dialog"]').waitFor({ state: 'detached', timeout: 10000 });
  }

  async moveMemberDown(index: number) {
    await this.page.locator(`[data-testid="layer-member-down-${index}"]`).click();
  }

  async moveMemberUp(index: number) {
    await this.page.locator(`[data-testid="layer-member-up-${index}"]`).click();
  }

  layerRow(scheduleName: string) {
    return this.scheduleRow(scheduleName).locator('[data-testid="layer-row"]');
  }

  async createOverride(
    scheduleName: string,
    data: {
      replacedUserId: number;
      replacedSearchTerm: string;
      replacementUserId: number;
      replacementSearchTerm: string;
      startTime: string;
      endTime: string;
    }
  ) {
    const row = this.scheduleRow(scheduleName);
    await row.locator('[data-testid="override-create"]').click();
    await this.page.waitForSelector('div[role="dialog"]', { timeout: 5000 });

    const dialog = this.page.locator('div[role="dialog"]');
    const triggers = dialog.locator('[data-testid="user-picker-trigger"]');

    await pickUser(this.page, triggers.nth(0), data.replacedUserId, data.replacedSearchTerm);
    await pickUser(this.page, triggers.nth(1), data.replacementUserId, data.replacementSearchTerm);

    await this.page.fill('#override-start', data.startTime);
    await this.page.fill('#override-end', data.endTime);

    await this.page.locator('[data-testid="override-save"]').click();
    await this.page.locator('div[role="dialog"]').waitFor({ state: 'detached', timeout: 10000 });
  }

  /** Override rows rendered under a schedule's "Overrides" section. */
  overrideRow(scheduleName: string) {
    return this.scheduleRow(scheduleName).locator('[data-testid="override-row"]');
  }

  async expectScheduleVisible(name: string) {
    await expect(this.scheduleRow(name)).toBeVisible({ timeout: 10000 });
  }

  async expectLayerVisible(scheduleName: string, layerName: string) {
    const layer = this.scheduleRow(scheduleName).locator('[data-testid="layer-row"]', {
      hasText: layerName,
    });
    await expect(layer).toBeVisible({ timeout: 10000 });
  }
}
