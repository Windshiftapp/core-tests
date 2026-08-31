import AxeBuilder from '@axe-core/playwright';
import { createItemViaAPI, createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import type { APIRequestContext, Browser, BrowserContext, Page } from '../fixtures/context-path';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateUser, generateWorkspace } from '../fixtures/test-data';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

type RegionalSession = {
  context: BrowserContext;
  page: Page;
  userId: number;
};

async function assignEditor(request: APIRequestContext, userId: number, workspaceId: number) {
  const rolesResponse = await request.get('/api/workspace-roles', {
    headers: SEC_FETCH,
  });
  expect(rolesResponse.ok()).toBeTruthy();
  const rolesBody = await rolesResponse.json();
  const roles = rolesBody.data ?? rolesBody;
  const editor = roles.find((role: { name: string }) => role.name === 'Editor');
  expect(editor, 'seeded Editor role').toBeDefined();

  const assignResponse = await request.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: { user_id: userId, workspace_id: workspaceId, role_id: editor.id },
  });
  expect(assignResponse.ok()).toBeTruthy();
}

async function createRegionalSession(
  browser: Browser,
  adminRequest: APIRequestContext,
  workspaceId: number,
  options: {
    language: 'de' | 'ar';
    timezone: 'UTC' | 'Europe/Zurich';
  }
): Promise<RegionalSession> {
  const userData = generateUser(`matrix-${options.language}`);
  const user = await createUserViaAPI(adminRequest, userData);
  await assignEditor(adminRequest, user.id, workspaceId);

  const regionalResponse = await adminRequest.put(`/api/users/${user.id}/regional-settings`, {
    headers: SEC_FETCH,
    data: options,
  });
  expect(regionalResponse.ok()).toBeTruthy();

  const context = await browser.newContext({
    baseURL: BASE_URL,
    locale: options.language,
    timezoneId: options.timezone,
    viewport: { width: 1280, height: 800 },
  });
  const loginResponse = await context.request.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: {
      email_or_username: userData.username,
      password: userData.password_hash,
      remember_me: false,
    },
  });
  expect(loginResponse.ok()).toBeTruthy();

  return { context, page: await context.newPage(), userId: user.id };
}

function seriousViolations(results: Awaited<ReturnType<AxeBuilder['analyze']>>) {
  return results.violations
    .filter((violation) => ['serious', 'critical'].includes(violation.impact ?? ''))
    .map((violation) => ({
      id: violation.id,
      impact: violation.impact,
      targets: violation.nodes.map((node) => node.target),
    }));
}

test('critical desktop and mobile shells have no serious axe violations', {
  tag: '@critical-browser',
}, async ({ page }) => {
  await page.goto('/');
  await expect(page.getByTestId('homepage')).toBeVisible({ timeout: 15_000 });

  let results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .disableRules(['color-contrast'])
    .analyze();
  expect(seriousViolations(results)).toEqual([]);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/m');
  await expect(page.getByTestId('mobile-shell')).toBeVisible();
  results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .disableRules(['color-contrast'])
    .analyze();
  expect(seriousViolations(results)).toEqual([]);
});

test('empty desktop shell fits the viewport with expanded navigation', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(() => {
    localStorage.setItem('windshift-nav-expanded', 'true');
  });
  await page.goto('/');

  const homepage = page.getByTestId('homepage');
  await expect(homepage).toBeVisible({ timeout: 15_000 });
  await expect(homepage).toHaveAttribute('data-ready', 'true');

  await expect
    .poll(() =>
      page.evaluate(() => ({
        clientWidth: document.documentElement.clientWidth,
        scrollWidth: document.documentElement.scrollWidth,
        clientHeight: document.documentElement.clientHeight,
        scrollHeight: document.documentElement.scrollHeight,
      }))
    )
    .toEqual({
      clientWidth: 1280,
      scrollWidth: 1280,
      clientHeight: 800,
      scrollHeight: 800,
    });
});

test('global create is operable by keyboard and moves focus into the dialog', {
  tag: '@critical-browser',
}, async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#global-create-button')).toBeVisible({
    timeout: 15_000,
  });

  await page.keyboard.press('c');
  await expect(page.getByTestId('create-modal')).toBeVisible();
  await expect(page.locator('#work-item-title')).toBeFocused();

  await page.keyboard.press('Escape');
  await expect(page.getByTestId('create-modal')).toBeHidden();
  await expect(page.locator('#global-create-button')).toBeVisible();
});

test('mobile shell reflows at 320 CSS pixels and at 200 percent zoom', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 720 });
  await page.goto('/m');
  await expect(page.getByTestId('mobile-shell')).toBeVisible();
  await expect(page.getByTestId('mobile-nav')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);

  await page.setViewportSize({ width: 640, height: 720 });
  await page.evaluate(() => {
    document.documentElement.style.zoom = '200%';
  });
  await expect(page.getByTestId('mobile-search-open')).toBeVisible();
  await expect(page.getByTestId('mobile-create-fab')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
});

test('dark color scheme and reduced motion reach the critical mobile surface', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' });
  await page.addInitScript(() => {
    localStorage.setItem('windshift-color-mode', 'system');
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/m');
  await expect(page.getByTestId('mobile-shell')).toBeVisible();

  const media = await page.evaluate(() => ({
    colorMode: document.documentElement.dataset.colorMode,
    dark: matchMedia('(prefers-color-scheme: dark)').matches,
    reduced: matchMedia('(prefers-reduced-motion: reduce)').matches,
  }));
  expect(media).toEqual({ colorMode: 'dark', dark: true, reduced: true });
});

test('German/UTC and Arabic/Zurich journeys apply locale, direction, and DST-aware history', async ({
  browser,
  request,
}) => {
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace('regional-matrix'));
  const itemData = generateItem(workspace.id, 'regional-matrix');
  const item = await createItemViaAPI(request, workspace.id, {
    title: itemData.title,
    description: itemData.description,
  });
  const sessions: RegionalSession[] = [];
  const history = [
    {
      changed_at: '2026-10-25T00:30:00Z',
      user_id: 1,
      user_name: 'Matrix User',
      user_email: 'matrix@example.test',
      field_name: 'title',
      old_value: 'Before',
      new_value: 'After',
    },
    {
      changed_at: '2026-10-25T01:30:00Z',
      user_id: 1,
      user_name: 'Matrix User',
      user_email: 'matrix@example.test',
      field_name: 'description',
      old_value: 'Before',
      new_value: 'After',
    },
  ];

  try {
    for (const options of [
      { language: 'de' as const, timezone: 'UTC' as const },
      { language: 'ar' as const, timezone: 'Europe/Zurich' as const },
    ]) {
      const session = await createRegionalSession(browser, request, workspace.id, options);
      sessions.push(session);
      await session.page.route(`**/api/items/${item.id}/history`, (route) =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(history),
        })
      );

      await session.page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
      await expect(session.page.getByTestId('item-detail-ready')).toBeVisible({
        timeout: 15_000,
      });
      await expect(session.page.locator('#global-create-button')).toHaveAccessibleName(
        options.language === 'de' ? /Erstellen/ : /إنشاء/
      );
      expect(
        await session.page.evaluate(() => ({
          lang: document.documentElement.lang,
          dir: document.documentElement.dir,
        }))
      ).toEqual({
        lang: options.language,
        dir: options.language === 'ar' ? 'rtl' : 'ltr',
      });

      await session.page.getByTestId('item-detail-history-tab').click();
      const renderedTimes = session.page.getByTestId('item-history-time');
      await expect(renderedTimes).toHaveCount(2);
      const titles = await renderedTimes.evaluateAll((nodes) =>
        nodes.map((node) => node.getAttribute('title'))
      );
      const expected = await session.page.evaluate(
        ({ entries, timezone }) =>
          entries.map((entry) => {
            const date = new Date(entry.changed_at);
            const datePart = date.toLocaleDateString(document.documentElement.lang, {
              year: 'numeric',
              month: 'short',
              day: 'numeric',
              timeZone: timezone,
            });
            const timePart = date.toLocaleTimeString(document.documentElement.lang, {
              hour: 'numeric',
              minute: '2-digit',
              timeZone: timezone,
            });
            const zone = new Intl.DateTimeFormat(document.documentElement.lang, {
              timeZone: timezone,
              timeZoneName: 'short',
            })
              .formatToParts(date)
              .find((part) => part.type === 'timeZoneName')?.value;
            return `${datePart} at ${timePart} ${zone ?? ''}`.trim();
          }),
        { entries: history, timezone: options.timezone }
      );
      expect(titles).toEqual(expected);
      if (options.timezone === 'Europe/Zurich') {
        expect(titles[0]).not.toBe(titles[1]);
      }
    }
  } finally {
    for (const session of sessions) {
      await session.context.close();
      await request.delete(`/api/users/${session.userId}`, {
        headers: SEC_FETCH,
      });
    }
  }
});

test('mobile chat, service-worker update checks, and offline navigation recover deterministically', async ({
  context,
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('**/api/llm/connections', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
  );
  await page.route('**/api/ai/chat', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ answer: 'Deterministic mobile answer' }),
    })
  );
  await page.goto('/m/chat');
  await expect(page.getByTestId('mobile-chat-header')).toBeVisible();
  await page.getByTestId('chat-input').fill('Plan my day');
  await page.getByTestId('chat-input').press('Enter');
  await expect(page.getByTestId('chat-msg-user')).toHaveText('Plan my day');
  await expect(page.getByTestId('chat-msg-assistant')).toHaveText('Deterministic mobile answer');

  await page.goto('/m');
  await expect(page.getByTestId('mobile-nav')).toBeVisible();
  const worker = await page.evaluate(async () => {
    const registration = await navigator.serviceWorker.ready;
    await registration.update();
    return {
      active: registration.active?.state,
      controlled: Boolean(navigator.serviceWorker.controller),
    };
  });
  expect(worker).toEqual({ active: 'activated', controlled: true });

  await context.setOffline(true);
  try {
    const response = await page.goto('/m/search', {
      waitUntil: 'domcontentloaded',
    });
    expect(response?.status()).toBe(503);
    await expect(page.locator('#retry')).toBeVisible();
  } finally {
    await context.setOffline(false);
  }
  await expect(page.getByTestId('mobile-search-input')).toBeVisible({
    timeout: 15_000,
  });
});
