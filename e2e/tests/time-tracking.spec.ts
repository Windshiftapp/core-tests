import {
  createCustomerOrgViaAPI,
  createItemViaAPI,
  createTimeProjectViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateTimeProject, generateWorklog } from '../fixtures/test-data';
import { TimeTrackingPage } from '../pages/time-tracking.page';

/**
 * Time Tracking Tests
 * Tests time projects, worklogs, and the start/stop timer flow that runs
 * out of the work-item detail modal and the global FloatingTimer widget.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

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
      const resp = await request.get('/api/timer/active', {
        headers: SEC_FETCH,
      });
      if (resp.ok()) {
        const body = await resp.json().catch(() => null);
        const id = body?.id ?? body?.timer?.id;
        if (id) {
          await request.delete(`/api/timer/${id}/stop`, { headers: SEC_FETCH });
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
        (r) => r.url().includes('/api/timer/start') && r.request().method() === 'POST' && r.ok()
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
      const startResp = await request.post('/api/timer/start', {
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
          r.url().includes('/api/timer/') &&
          r.url().endsWith('/stop') &&
          r.request().method() === 'DELETE' &&
          r.ok()
      );
      await page.locator('[data-testid="stop-timer-btn"]').click();
      await stopResp;

      await expect(floating).toBeHidden({ timeout: 5000 });
    });
  });
});
