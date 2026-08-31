import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

/**
 * Hub portal-card gradient rendering.
 *
 * `gradients[0]` ("None") has a `null` value, so the card used to render
 * `background: null;` when a portal had `portal_gradient: 0` saved (the
 * default for new portals). The card now mirrors PortalHero's resolution:
 * fall back to `gradients[1]` (Blue to Purple) when the selected gradient
 * has no value.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function createPortalChannel(
  request: import('@playwright/test').APIRequestContext,
  name: string,
  slug: string,
  gradient: number
): Promise<void> {
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(slug));
  const resp = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name,
      type: 'portal',
      direction: 'inbound',
      status: 'disabled',
      slug,
    },
  });
  expect(
    resp.ok(),
    `create portal channel ${name}: ${resp.status()} ${await resp.text()}`
  ).toBeTruthy();
  const channel = await resp.json();

  const configResp = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        portal_slug: slug,
        portal_workspace_ids: [workspace.id],
        portal_title: name,
        portal_gradient: gradient,
        portal_registration_mode: 'open',
      },
    },
  });
  expect(configResp.ok(), `configure portal ${name}: ${configResp.status()}`).toBeTruthy();

  const toggleResp = await request.put(`/api/channels/${channel.id}/toggle`, {
    headers: SEC_FETCH,
  });
  expect(toggleResp.ok(), `enable portal ${name}: ${toggleResp.status()}`).toBeTruthy();
}

test.describe('Hub portal cards render a gradient', () => {
  test('cards fall back to a real gradient when portal_gradient is 0', async ({
    page,
    request,
  }) => {
    const stamp = Date.now();
    const noneName = `Hub Card None ${stamp}`;
    const noneSlug = `hub-card-none-${stamp}`;
    const sunsetName = `Hub Card Sunset ${stamp}`;
    const sunsetSlug = `hub-card-sunset-${stamp}`;

    await createPortalChannel(request, noneName, noneSlug, 0);
    await createPortalChannel(request, sunsetName, sunsetSlug, 3);

    // Other hub specs customize sections globally. Reset to the default
    // unsectioned view so every enabled portal is rendered as a card.
    const hubResp = await request.get('/api/hub', { headers: SEC_FETCH });
    expect(hubResp.ok()).toBeTruthy();
    const hub = await hubResp.json();
    const configResp = await request.put('/api/hub/config', {
      headers: SEC_FETCH,
      data: { ...hub.config, sections: [] },
    });
    expect(configResp.ok()).toBeTruthy();

    // The portal hub view is mounted at /channels (see frontend/src/lib/router.js).
    await page.goto('/channels');

    const noneCard = page.getByTestId(`hub-portal-card-${noneSlug}`);
    const sunsetCard = page.getByTestId(`hub-portal-card-${sunsetSlug}`);

    await expect(noneCard).toBeVisible();
    await expect(sunsetCard).toBeVisible();

    // gradient=0 falls back to gradients[1] = "Blue to Purple"
    await expect(page.getByTestId(`hub-portal-card-gradient-${noneSlug}`)).toHaveCSS(
      'background-image',
      /linear-gradient.*(?:667eea|102,\s*126,\s*234)/i
    );

    // gradient=3 = "Sunset Warmth" → #FF6B6B → #FFE66D
    await expect(page.getByTestId(`hub-portal-card-gradient-${sunsetSlug}`)).toHaveCSS(
      'background-image',
      /linear-gradient.*(?:ff6b6b|255,\s*107,\s*107)/i
    );
  });
});
