import { expect, type Locator, type Page } from '../fixtures/context-path';

/**
 * Open a UserPicker trigger, type a search term, and click the option with the
 * given user id. UserPicker portals its popover to <body> (not the dialog), and
 * shows only the first 4 users by default — searching is required to surface
 * a newly-created e2e user.
 */
export async function pickUser(page: Page, trigger: Locator, userId: number, searchTerm: string) {
  await trigger.click();
  const search = page.locator('[data-testid="user-picker-search"]');
  await search.waitFor({ state: 'visible', timeout: 5000 });
  await search.fill(searchTerm);
  const option = page.locator(`[data-testid="user-picker-option-${userId}"]`);
  await option.waitFor({ state: 'visible', timeout: 10000 });
  await option.click();
}

/**
 * Page Object for the top-level Teams feature at /teams and /teams/:id.
 *
 * The Teams feature is distinct from the older /admin/groups admin UI;
 * the latter is covered by GroupPage. Teams have on-call schedules attached;
 * see TeamOnCallPage for those flows.
 */
export class TeamsPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  readonly teamModal = 'div[role="dialog"]';
  readonly teamRow = 'tbody tr';
  readonly nameInput = '#team-name';
  readonly descriptionInput = '#team-description';
  readonly createButton = '[data-testid="team-create-button"]';
  readonly saveButton = '[data-testid="team-save"]';

  async goto() {
    await this.page.goto('/teams');
    await this.page.waitForLoadState('networkidle');
  }

  async clickCreate() {
    await this.page.click(this.createButton);
    await this.page.waitForSelector(this.teamModal, { timeout: 5000 });
  }

  async fillForm(data: { name: string; description?: string }) {
    await this.page.fill(this.nameInput, data.name);
    if (data.description !== undefined) {
      await this.page.fill(this.descriptionInput, data.description);
    }
  }

  async clickSave() {
    const dialog = this.page.locator(this.teamModal);
    await dialog.locator(this.saveButton).click();
    await dialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  async createTeam(data: { name: string; description?: string }) {
    await this.goto();
    await this.clickCreate();
    await this.fillForm(data);
    await this.clickSave();
  }

  findTeamRow(name: string) {
    return this.page.locator(`${this.teamRow}:has-text("${name}")`).first();
  }

  async verifyTeamExists(name: string) {
    await this.goto();
    await expect(this.findTeamRow(name)).toBeVisible({ timeout: 10000 });
  }

  async verifyTeamDoesNotExist(name: string) {
    await expect(this.page.locator(`${this.teamRow}:has-text("${name}")`)).not.toBeVisible({
      timeout: 5000,
    });
  }

  async openTeam(name: string) {
    await this.findTeamRow(name).click();
    await this.page.waitForLoadState('networkidle');
  }

  private async openRowDropdown(name: string) {
    const row = this.findTeamRow(name);
    await row.locator('button').last().click();
    await this.page
      .locator('button[role="menuitem"]')
      .first()
      .waitFor({ state: 'visible', timeout: 5000 });
  }

  private async clickMenuItem(text: string) {
    await this.page.locator('button[role="menuitem"]').filter({ hasText: text }).click();
  }

  async editTeam(currentName: string, newData: { name?: string; description?: string }) {
    await this.goto();
    await this.openRowDropdown(currentName);
    await this.clickMenuItem('Edit');
    await this.page.waitForSelector(this.teamModal, { timeout: 5000 });
    if (newData.name) await this.page.fill(this.nameInput, newData.name);
    if (newData.description !== undefined)
      await this.page.fill(this.descriptionInput, newData.description);
    await this.clickSave();
  }

  async deleteTeam(name: string) {
    await this.goto();
    await this.openRowDropdown(name);
    await this.clickMenuItem('Delete');

    const confirmDialog = this.page.locator(this.teamModal);
    await confirmDialog.waitFor({ state: 'visible', timeout: 5000 });
    await confirmDialog.locator('button:has-text("Delete")').last().click();
    await confirmDialog.waitFor({ state: 'detached', timeout: 10000 });
  }

  async openMembersTab(teamName: string) {
    await this.goto();
    await this.openTeam(teamName);
    await this.page.locator('[data-testid="team-tab-members"]').click();
    await this.page.waitForLoadState('networkidle');
  }

  async addMember(teamName: string, userId: number, searchTerm: string) {
    await this.openMembersTab(teamName);
    // [data-testid="team-add-member"] *opens* the picker modal; the picker
    // (UserPicker) is mounted inside that modal. The confirm button is
    // [data-testid="add-member-confirm"] (DialogFooter in MembersTab.svelte).
    await this.page.locator('[data-testid="team-add-member"]').click();
    const modal = this.page.locator('[role="dialog"]');
    await modal.waitFor({ state: 'visible', timeout: 5000 });
    await pickUser(
      this.page,
      modal.locator('[data-testid="user-picker-trigger"]'),
      userId,
      searchTerm
    );
    await this.page.locator('[data-testid="add-member-confirm"]').click();
    await this.page
      .locator(`[data-testid="member-row"][data-user-id="${userId}"]`)
      .waitFor({ state: 'visible', timeout: 10000 });
  }

  async openGroupsTab(teamName: string) {
    await this.goto();
    await this.openTeam(teamName);
    await this.page.locator('[data-testid="team-tab-groups"]').click();
    await this.page.waitForLoadState('networkidle');
  }

  async attachGroup(teamName: string, groupId: number) {
    await this.openGroupsTab(teamName);
    // Same shape as addMember: open-modal button vs. confirm button are
    // separate (`team-add-group` opens, `add-group-confirm` commits — see
    // GroupsTab.svelte:119,156). Group picker is BasePicker, not UserPicker.
    await this.page.locator('[data-testid="team-add-group"]').click();
    await this.page.locator('[role="dialog"]').waitFor({ state: 'visible', timeout: 5000 });
    await this.pickFromBasePicker(0, groupId);
    await this.page.locator('[data-testid="add-group-confirm"]').click();
    await this.page.locator('tbody tr').first().waitFor({ state: 'visible', timeout: 10000 });
  }

  /**
   * Pick an option from a BasePicker-backed combobox by its numeric value.
   * BasePicker renders each option with `data-option-value="<id>"`. UserPicker
   * is a different component — use {@link pickUser} for those.
   */
  private async pickFromBasePicker(comboIndex: number, optionValue: number | string) {
    const combo = this.page.getByRole('combobox').nth(comboIndex);
    await combo.click();
    await this.page
      .locator(`[data-option-value]`)
      .first()
      .waitFor({ state: 'visible', timeout: 15000 });
    const option = this.page.locator(`[data-option-value="${optionValue}"]`).first();
    await option.waitFor({ state: 'visible', timeout: 15000 });
    await option.click();
  }

  async getTeamCount(): Promise<number> {
    await this.goto();
    return this.page.locator(this.teamRow).count();
  }

  /** Read the numeric team id from the URL after openTeam(). */
  async getTeamIdFromUrl(): Promise<number> {
    const url = this.page.url();
    const match = url.match(/\/teams\/(\d+)/);
    if (!match) throw new Error(`Not on a team detail URL: ${url}`);
    return Number(match[1]);
  }
}
