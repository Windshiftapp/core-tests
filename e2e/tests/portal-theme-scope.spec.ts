import { authenticateAdminRequest } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { createPortalChannel } from '../helpers/portal-setup';

/**
 * GitHub #375 — the portal's light/dark theme must stay scoped to the portal.
 *
 * The portal used to write `document.documentElement.dataset.colorMode` from its
 * own theme. "Back to App" is a client-side `navigate('/')`; the app theme store
 * only applies on init or an audience change, so the portal theme stuck until a
 * reload. This spec pins the reported flow: a dark app, a light portal, and a
 * dark app again after Back to App.
 */

const DARK_SURFACE = 'rgb(29, 33, 37)'; // --ds-surface under [data-color-mode="dark"]
const LIGHT_SURFACE = 'rgb(250, 251, 252)'; // --ds-surface under light tokens

test('portal theme does not leak into the app on Back to App', async ({ page, request }) => {
  await authenticateAdminRequest(request);

  const stamp = Date.now();
  const slug = `e2e-theme-${stamp}`;
  const channel = await createPortalChannel(request, {
    slug,
    name: `Theme scope ${stamp}`,
  });

  // Pin the portal to light so the assertion does not depend on the seeded
  // default and the contrast with the dark app is unambiguous.
  const configResponse = await request.put(`/api/channels/${channel.channelId}/config`, {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
    data: { config: { portal_theme: 'light' } },
  });
  expect(
    configResponse.ok(),
    `set portal theme: ${configResponse.status()} ${await configResponse.text()}`
  ).toBeTruthy();

  // Dark app color mode for the admin session.
  await page.addInitScript(() => {
    window.localStorage.setItem('windshift-color-mode', 'dark');
  });

  await page.goto('/');
  await expect(page.getByTestId('nav-logo-link')).toBeVisible();
  await expect(page.locator('#app-main')).toHaveCSS('background-color', DARK_SURFACE);

  // Opening the light portal must not mutate the document root.
  await page.goto(`/portal/${slug}`);
  await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');
  await expect(page.locator('#app-main')).toHaveCSS('background-color', DARK_SURFACE);
  await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ds-color-mode', 'light');
  await expect(page.getByTestId('portal-page')).toHaveCSS('background-color', LIGHT_SURFACE);

  // Back to App is a client-side navigation; the app must still be dark.
  await page.getByTestId('portal-settings-button').click();
  await page.getByTestId('portal-back-to-app').click();
  await expect(page.getByTestId('nav-logo-link')).toBeVisible();
  await expect(page.locator('#app-main')).toHaveCSS('background-color', DARK_SURFACE);
});
