import { authenticateAdminRequest, createCustomerOrgViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/mail';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createPortalCustomer,
  createSimpleRequestType,
  setOrganisationRequestSharing,
  signInViaMagicLink,
} from '../helpers/portal-setup';

/**
 * WI-1139 portal organisation request sharing, end to end through the UI.
 *
 * Two specs:
 * - the admin configures the policy on the organisation Sharing tab and it
 *   persists across a reload;
 * - with requester_choice enabled, the creator opts in from the request form
 *   and a colleague in the same organisation sees the shared request labelled,
 *   while a request the creator left private stays invisible.
 *
 * The portal half needs Mailpit for the magic-link sign-in; it skips when
 * Mailpit is unavailable. State is created through the production API.
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

test.describe('Portal organisation request sharing (WI-1139)', () => {
  test('admin configures organisation request sharing and it persists', async ({
    request,
    page,
  }) => {
    await authenticateAdminRequest(request);

    const stamp = Date.now();
    const org = await createCustomerOrgViaAPI(request, { name: `Admin Sharing Org ${stamp}` });

    const openSharingTab = async () => {
      await page.goto('/organizations');
      await page
        .getByTestId('customer-organization')
        .filter({ hasText: org.name })
        .click();
      await page.getByTestId('org-detail-sharing-tab').click();
      await expect(page.getByTestId('org-request-sharing')).toBeVisible();
    };

    await openSharingTab();

    // Switch to requester_choice and save.
    await page.locator('#org-sharing-mode').click();
    await page.locator('#org-sharing-mode-option-requester_choice').click();
    const saveResponse = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/customer-organisations/${org.id}/request-sharing`) &&
        resp.request().method() === 'PUT'
    );
    await page.getByTestId('org-sharing-save').click();
    expect((await saveResponse).ok()).toBeTruthy();

    // Reload and confirm the selection persisted server-side.
    await openSharingTab();
    await expect(page.locator('#org-sharing-mode')).toContainText(/choose per request/i);
  });

  test('creator shares a request and a colleague sees it, while a private request stays hidden', {
    tag: '@critical-browser',
  }, async ({ request, mail, page }) => {
    mail.skipIfMissing();
    await authenticateAdminRequest(request);

    const stamp = Date.now();
    const slug = `e2e-org-share-${stamp}`;
    const creatorEmail = `e2e-share-creator-${stamp}@windshift.test`;
    const colleagueEmail = `e2e-share-colleague-${stamp}@windshift.test`;
    const sharedTitle = `Shared request ${stamp}`;
    const privateTitle = `Private request ${stamp}`;

    const channel = await createPortalChannel(request, { slug, name: `Sharing ${stamp}` });
    const rt = await createSimpleRequestType(request, channel, {
      name: 'General request',
    });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    const org = await createCustomerOrgViaAPI(request, { name: `Portal Sharing Org ${stamp}` });
    await createPortalCustomer(request, {
      name: 'Creator',
      email: creatorEmail,
      organisationId: org.id,
    });
    await createPortalCustomer(request, {
      name: 'Colleague',
      email: colleagueEmail,
      organisationId: org.id,
    });
    await setOrganisationRequestSharing(request, org.id, {
      request_sharing: 'requester_choice',
      request_sharing_audience: 'organisation',
    });

    const signIn = async (email: string) => {
      await page.context().clearCookies();
      await page.goto(`${BASE_URL}/portal/${slug}`);
      await page.locator('#portal-sign-in').click();
      await signInViaMagicLink(page, { mail, slug, email });
      await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');
    };

    const submit = async (title: string, share: boolean) => {
      await page.goto(`${BASE_URL}/portal/${slug}`);
      const card = page.getByTestId('portal-request-type-card');
      await expect(card).toBeVisible();
      await card.click();
      await page.locator('#request-title').fill(title);
      if (share) {
        await page.getByTestId('request-form-share-with-organisation').click();
      }
      const submitResponse = page.waitForResponse(
        (resp) =>
          resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
      );
      await page.getByTestId('request-form-submit').click();
      const resp = await submitResponse;
      expect(resp.ok(), `submit: ${resp.status()}`).toBeTruthy();
    };

    // Creator: one shared request, one private request.
    await signIn(creatorEmail);
    await submit(sharedTitle, true);
    await submit(privateTitle, false);

    // Colleague: sees the shared request (labelled), not the private one.
    await signIn(colleagueEmail);
    await page.goto(`${BASE_URL}/portal/${slug}?view=requests`);

    const rows = page.getByTestId('portal-my-requests-row');
    const sharedRow = rows.filter({ hasText: sharedTitle });
    await expect(sharedRow).toBeVisible();
    await expect(sharedRow.getByTestId('portal-request-shared')).toBeVisible();
    await expect(rows.filter({ hasText: privateTitle })).toHaveCount(0);
  });
});
