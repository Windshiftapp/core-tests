import {
  createCustomerOrgViaAPI,
  createItemViaAPI,
  createTimeProjectViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test, type Page } from '../fixtures/context-path';
import { generateTimeProject, generateWorklog } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';
import { TimeTrackingPage } from '../pages/time-tracking.page';

/**
 * Time Tracking Tests
 * Tests time projects, worklogs, and the start/stop timer flow that runs
 * out of the work-item detail modal and the global FloatingTimer widget.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

// Distinct civil dates in the current UTC month, excluding today. The default
// Time filter shows the current month, and today's created_at must differ from
// the work date so a list that ignores the work date is observable.
function currentMonthWorkDates(count: number): string[] {
  const now = new Date();
  const year = now.getUTCFullYear();
  const month = now.getUTCMonth() + 1;
  const today = now.getUTCDate();
  const dates: string[] = [];
  for (let day = 1; day <= 28 && dates.length < count; day += 1) {
    if (day === today) continue;
    dates.push(`${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`);
  }
  return dates;
}

// Mirror the list's date label (app locale, UTC civil date) in the browser.
async function civilDateLabel(page: Page, ymd: string): Promise<string> {
  return page.evaluate((value) => {
    const [year, month, day] = value.split('-').map(Number);
    return new Intl.DateTimeFormat(document.documentElement.lang || 'en', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      timeZone: 'UTC',
    }).format(new Date(Date.UTC(year, month - 1, day)));
  }, ymd);
}

test.describe('Time Tracking', () => {
  let timeTrackingPage: TimeTrackingPage;
  let setupProjectName: string;
  let setupProjectId: number;
  let customerName: string;

  test.beforeAll(async ({ request }, workerInfo) => {
    const fixtureSuffix = `${workerInfo.workerIndex}-${Date.now()}`;
    customerName = `E2E Time Customer ${fixtureSuffix}`;
    // Create a customer organisation + project via API to bypass the onboarding wizard
    const customer = await createCustomerOrgViaAPI(request, {
      name: customerName,
      active: true,
    });
    const customerData = customer.data || customer;

    const project = await createTimeProjectViaAPI(request, {
      name: `E2E Setup Project ${fixtureSuffix}`,
      customer_id: customerData.id,
    });
    const projectData = project.data || project;
    setupProjectName = projectData.name;
    setupProjectId = projectData.id;
  });

  test.beforeEach(async ({ page }) => {
    timeTrackingPage = new TimeTrackingPage(page);
  });

  test.describe('Time Projects', () => {
    test('should create a time project', async () => {
      const project = generateTimeProject();
      await timeTrackingPage.createProject({
        ...project,
        customer: customerName,
      });
      await timeTrackingPage.verifyProjectExists(project.name);
    });

    test('should create project with description', async () => {
      const project = generateTimeProject('with-desc');
      await timeTrackingPage.createProject({
        name: project.name,
        description: project.description,
        customer: customerName,
      });
      await timeTrackingPage.verifyProjectExists(project.name);
    });

    test('should display project in list', async () => {
      const project = generateTimeProject('list');
      await timeTrackingPage.createProject({
        ...project,
        customer: customerName,
      });

      await timeTrackingPage.gotoProjects();
      const projectRow = timeTrackingPage.findProjectByName(project.name);
      await expect(projectRow).toBeVisible();
      await expect(projectRow).toContainText(project.name);
    });

    test('should delete a time project', async () => {
      const project = generateTimeProject('delete');
      await timeTrackingPage.createProject({
        ...project,
        customer: customerName,
      });
      await timeTrackingPage.verifyProjectExists(project.name);

      await timeTrackingPage.deleteProject(project.name);

      await timeTrackingPage.gotoProjects();
      const projectRow = timeTrackingPage.findProjectByName(project.name);
      await expect(projectRow).not.toBeVisible({ timeout: 5000 });
    });
  });

  test.describe('Worklogs', () => {
    test('should log time', async () => {
      const worklog = generateWorklog();
      await timeTrackingPage.goto();

      await timeTrackingPage.logTime({
        project: setupProjectName,
        description: worklog.description,
        duration: '1h',
        date: worklog.date,
      });

      await timeTrackingPage.verifyWorklogExists(worklog.description);
    });

    test('should display worklog in list', async () => {
      const worklog = generateWorklog('display');
      await timeTrackingPage.goto();

      await timeTrackingPage.logTime({
        project: setupProjectName,
        description: worklog.description,
        duration: '30m',
      });

      const worklogRow = timeTrackingPage.findWorklogByDescription(worklog.description);
      await expect(worklogRow).toBeVisible();
    });

    test('reflects an edited date and time in the list', async ({ page }) => {
      const description = `E2E Worklog edit ${Date.now()}`;
      const [workDate, editedDate] = currentMonthWorkDates(2);

      await timeTrackingPage.goto();
      await timeTrackingPage.logTime({
        project: setupProjectName,
        description,
        duration: '1h',
        date: workDate,
        startTime: '09:00',
        endTime: '10:00',
      });

      const row = timeTrackingPage.findWorklogByDescription(description);
      await expect(row).toBeVisible({ timeout: 10_000 });
      // The list shows the work date, not the created_at date.
      await expect(row).toContainText(await civilDateLabel(page, workDate));

      await row.getByTestId(/^worklog-actions-/).click();
      await page.getByTestId('worklog-edit').click();

      const dialog = page.getByTestId('time-log-modal');
      await expect(dialog).toBeVisible();
      await dialog.locator('#time-log-date').fill(editedDate);
      await dialog.locator('#time-log-start-time').fill('11:00');
      await dialog.locator('#time-log-end-time').fill('12:30');
      await dialog.getByTestId('dialog-confirm').click();
      await dialog.waitFor({ state: 'detached', timeout: 10_000 });

      await expect(row).toContainText(await civilDateLabel(page, editedDate));
      await expect(row).toContainText('11:00');
      await expect(row).toContainText('12:30');
    });

    test('exports a Markdown time report through the printable Milkdown view', async ({ page }) => {
      const marker = `Printable report ${Date.now()}`;
      const description = `${marker} # not a heading | ![pixel](https://example.com/pixel.png) \`code\``;

      await page.context().addInitScript(() => {
        // @ts-expect-error neutralize the native print dialog in headless runs
        window.print = () => {};
      });
      await timeTrackingPage.goto();
      await timeTrackingPage.logTime({
        project: setupProjectName,
        description,
        duration: '45m',
      });

      await page.goto('/time/worklogs');
      const exportButton = page.getByTestId('time-report-export-pdf');
      await expect(exportButton).toBeEnabled({ timeout: 15_000 });

      const popupPromise = page.waitForEvent('popup');
      await exportButton.click();
      const popup = await popupPromise;
      await expect.poll(() => popup.url()).toMatch(/\/time\/worklogs\/print$/);

      const reportBody = popup.getByTestId('time-report-print-body');
      await expect(reportBody).toContainText('Time Tracking Report', {
        timeout: 15_000,
      });
      await expect(reportBody).toContainText(marker);
      await expect(reportBody).toContainText('# not a heading');
      expect(await reportBody.evaluate((body) => body.querySelectorAll('img, a').length)).toBe(0);
      await expect(popup.getByTestId('time-report-print-button')).toBeVisible();

      await popup.emulateMedia({ media: 'print' });
      await expect(popup.getByTestId('time-report-print-button')).toBeHidden();
      await popup.close();
    });
  });

  test.describe('Timer', () => {
    test.describe.configure({ mode: 'serial' });

    // Timer lives in the work-item detail modal (Start) and the global
    // FloatingTimer widget (Stop). Both require the workspace to advertise a
    // default time project so `getDefaultProjectForTimeLogging()` resolves —
    // otherwise the Start Timer button is conditionally hidden.

    test.afterEach(async ({ request }) => {
      // Tests share a single admin session, so a running timer from one case
      // would block the next. Best-effort stop via API.
      const resp = await request.get('/api/v2/time/timers/active', {
        headers: SEC_FETCH,
      });
      if (resp.ok()) {
        const body = await resp.json().catch(() => null);
        if (body?.data) {
          await request.post('/api/v2/time/timers/active/stop', {
            headers: SEC_FETCH,
          });
        }
      }
    });

    test('should start timer from the item detail modal', async ({ page, request }) => {
      const stamp = Date.now();
      const ws = await createWorkspaceViaAPI(request, {
        name: `E2E Timer Start ${stamp}`,
        key: `TS${stamp.toString().slice(-5)}`,
        description: 'workspace for timer-start spec',
        time_project_id: setupProjectId,
      });
      const workspaceId = ws.id ?? ws.data?.id;
      const item = await createItemViaAPI(request, workspaceId, {
        title: `Timer start item ${stamp}`,
        description: 'item used by the start-timer e2e',
      });
      const itemId = item.id ?? item.data?.id;

      await page.goto(`/workspaces/${workspaceId}/items/${itemId}`);
      // Deep-link route renders ItemDetail inline (isModal=false), not in a
      // Modal — wait on the tab testid which exists in both render paths.
      const timeTab = page.getByTestId('item-detail-time-tab');
      await expect(timeTab).toBeVisible({ timeout: 10000 });
      await timeTab.click();
      const startBtn = page.getByTestId('start-timer-btn');
      await expect(startBtn).toBeVisible({ timeout: 5000 });

      const startResp = page.waitForResponse(
        (r) => r.url().endsWith('/api/v2/time/timers') && r.request().method() === 'POST' && r.ok()
      );
      await startBtn.click();
      await startResp;

      await expect(page.getByTestId('floating-timer')).toBeVisible({
        timeout: 5000,
      });
    });

    test('should stop a running timer from the floating widget', async ({ page, request }) => {
      const stamp = Date.now();
      const ws = await createWorkspaceViaAPI(request, {
        name: `E2E Timer Stop ${stamp}`,
        key: `TP${stamp.toString().slice(-5)}`,
        description: 'workspace for timer-stop spec',
        time_project_id: setupProjectId,
      });
      const workspaceId = ws.id ?? ws.data?.id;
      const item = await createItemViaAPI(request, workspaceId, {
        title: `Timer stop item ${stamp}`,
        description: 'item used by the stop-timer e2e',
      });
      const itemId = item.id ?? item.data?.id;

      // Seed the running timer via API so the test focuses on the Stop flow.
      const startResp = await request.post('/api/v2/time/timers', {
        headers: SEC_FETCH,
        data: {
          workspace_id: workspaceId,
          item_id: itemId,
          project_id: setupProjectId,
          description: 'stop-timer e2e seed',
        },
      });
      expect(startResp.ok(), `start timer: ${startResp.status()}`).toBeTruthy();

      // Any authenticated page surfaces the FloatingTimer (it's a global widget).
      await page.goto('/time');
      const floating = page.locator('[data-testid="floating-timer"]');
      await expect(floating).toBeVisible({ timeout: 10000 });

      const stopResp = page.waitForResponse(
        (r) =>
          r.url().endsWith('/api/v2/time/timers/active/stop') &&
          r.request().method() === 'POST' &&
          r.ok()
      );
      await page.locator('[data-testid="stop-timer-btn"]').click();
      await stopResp;

      await expect(floating).toBeHidden({ timeout: 5000 });
    });
  });

  test.describe('Worklogs from the item detail modal', () => {
    test('should log time from the item detail modal', async ({ page, request }, testInfo) => {
      const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
      const workspace = await createWorkspaceViaAPI(request, {
        name: `E2E Modal Log ${stamp}`,
        key: `ML${stamp.slice(-6)}`.toUpperCase(),
        description: 'workspace for item-modal log-time spec',
        time_project_id: setupProjectId,
      });
      const item = await createItemViaAPI(request, workspace.id, {
        title: `Modal log item ${stamp}`,
        description: 'item used by the log-time-from-modal e2e',
      });

      const boardPage = new BoardPage(page);
      await boardPage.goto(String(workspace.id));
      await boardPage.verifyCardExists(item.title);
      await boardPage.clickCard(item.title);

      const dialog = page.getByTestId('item-detail-modal');
      await expect(dialog).toBeVisible({ timeout: 10_000 });
      await dialog.getByTestId('item-detail-time-tab').click();

      // Regression: the Log Time modal used to be rendered only in the
      // full-page view, so clicking this button in the modal did nothing.
      await dialog.getByTestId('log-time-btn').click();
      const timeModal = page.getByTestId('time-log-modal');
      await expect(timeModal).toBeVisible({ timeout: 5_000 });

      const description = `Modal worklog ${stamp}`;
      await timeModal.locator('#time-log-description').fill(description);
      await timeModal.locator('#time-log-duration').fill('45m');

      const saveResp = page.waitForResponse(
        (r) =>
          r.url().endsWith('/api/v2/time/worklogs') &&
          r.request().method() === 'POST' &&
          r.ok(),
      );
      await timeModal.getByTestId('dialog-confirm').click();
      await saveResp;
      await expect(timeModal).toBeHidden({ timeout: 5_000 });

      // The new entry is listed in the modal's time tab without a reload.
      await expect(dialog).toContainText(description, { timeout: 10_000 });
    });
  });
});
