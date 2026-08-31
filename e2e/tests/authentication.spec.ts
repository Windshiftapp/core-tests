import { expect, test } from '../fixtures/context-path';
import { LoginPage } from '../pages/login.page';

/**
 * Authentication Tests
 * Tests login, logout, and session management
 */

test.describe('Authentication', () => {
  test.describe('Login', () => {
    test.use({ storageState: { cookies: [], origins: [] } }); // No auth state

    test('should display login dialog when not authenticated', async ({ page }) => {
      const loginPage = new LoginPage(page);
      await loginPage.goto();

      // Login dialog should be visible
      const isVisible = await loginPage.isLoginDialogVisible();
      expect(isVisible).toBeTruthy();
    });

    test('should login with valid credentials', {
      tag: '@critical-browser',
    }, async ({ page }) => {
      const loginPage = new LoginPage(page);

      await loginPage.login('admin', 'TestPass123!');

      // Verify successful login
      await loginPage.verifyLoginSuccess();
    });

    test('should fail with invalid username', async ({ page }) => {
      const loginPage = new LoginPage(page);
      await loginPage.goto();

      await loginPage.fillUsername('invaliduser');
      await loginPage.fillPassword('SomePassword123!');
      const loginResponse = page.waitForResponse((res) => res.url().includes('/api/auth/login'), {
        timeout: 10000,
      });
      await loginPage.clickLogin();
      const resp = await loginResponse;
      expect([401, 403], `expected 4xx for invalid username, got ${resp.status()}`).toContain(
        resp.status()
      );

      expect(await loginPage.isLoginDialogVisible()).toBeTruthy();
      // The LoginDialog renders the auth-store error in an AlertBox; the
      // server returns "Authentication required" on a 401, and the FE falls
      // back to "Login failed" if the message is missing. Match either so the
      // assertion survives copy tweaks.
      await expect(
        page.getByText(/authentication required|login failed|invalid/i).first()
      ).toBeVisible({ timeout: 5000 });
    });

    test('should fail with invalid password', async ({ page }) => {
      const loginPage = new LoginPage(page);
      await loginPage.goto();

      await loginPage.fillUsername('admin');
      await loginPage.fillPassword('WrongPassword!');
      const loginResponse = page.waitForResponse((res) => res.url().includes('/api/auth/login'), {
        timeout: 10000,
      });
      await loginPage.clickLogin();
      const resp = await loginResponse;
      expect([401, 403], `expected 4xx for invalid password, got ${resp.status()}`).toContain(
        resp.status()
      );

      expect(await loginPage.isLoginDialogVisible()).toBeTruthy();
      // The LoginDialog renders the auth-store error in an AlertBox; the
      // server returns "Authentication required" on a 401, and the FE falls
      // back to "Login failed" if the message is missing. Match either so the
      // assertion survives copy tweaks.
      await expect(
        page.getByText(/authentication required|login failed|invalid/i).first()
      ).toBeVisible({ timeout: 5000 });
    });

    test('remember-me extends session cookie expiry far beyond a normal login', async ({
      browser,
    }) => {
      // Plain login: capture the session cookie's expiry in an isolated context
      // so the fresh cookie jar starts empty.
      const plainCtx = await browser.newContext({
        storageState: { cookies: [], origins: [] },
      });
      let plainSessionExpires: number | undefined;
      try {
        const plainLogin = new LoginPage(await plainCtx.newPage());
        await plainLogin.login('admin', 'TestPass123!', false);
        await plainLogin.verifyLoginSuccess();
        const plainSession = (await plainCtx.cookies()).find(
          (c) => c.name === 'session' || c.name === 'windshift_session'
        );
        if (!plainSession) {
          throw new Error('plain login should set a session cookie');
        }
        plainSessionExpires = plainSession.expires;
      } finally {
        await plainCtx.close();
      }

      // Remember-me login: same in a fresh context, then compare expiries.
      const rememberCtx = await browser.newContext({
        storageState: { cookies: [], origins: [] },
      });
      try {
        const rememberLogin = new LoginPage(await rememberCtx.newPage());
        await rememberLogin.login('admin', 'TestPass123!', true);
        await rememberLogin.verifyLoginSuccess();
        const rememberSession = (await rememberCtx.cookies()).find(
          (c) => c.name === 'session' || c.name === 'windshift_session'
        );
        if (!rememberSession) {
          throw new Error('remember-me login should set a session cookie');
        }
        if (plainSessionExpires === undefined) {
          throw new Error('plain login session expiry was not captured');
        }

        // A non-remembered session cookie is either browser-session (expires=-1)
        // or short-lived; a remembered one persists for at least the
        // SessionDuration window. Pin the difference to >1 week so a regression
        // that ignores the "Stay signed in" flag fails the spec.
        const ONE_WEEK = 7 * 24 * 3600;
        expect(rememberSession.expires - plainSessionExpires).toBeGreaterThan(ONE_WEEK);
      } finally {
        await rememberCtx.close();
      }
    });
  });

  test.describe('Logout', () => {
    test.use({ storageState: { cookies: [], origins: [] } });

    test('should logout successfully', async ({ page }) => {
      const loginPage = new LoginPage(page);

      // First login
      await loginPage.login('admin', 'TestPass123!');
      await loginPage.verifyLoginSuccess();

      // Then logout
      await loginPage.logout();
      await loginPage.verifyLogoutSuccess();
    });

    test('should clear session after logout', async ({ page }) => {
      const loginPage = new LoginPage(page);

      // Login
      await loginPage.login('admin', 'TestPass123!');
      await loginPage.verifyLoginSuccess();

      // Logout
      await loginPage.logout();

      // Verify session is cleared
      const cookies = await page.context().cookies();
      const sessionCookie = cookies.find(
        (c) => c.name === 'session' || c.name === 'windshift_session'
      );

      // Session cookie should be removed or expired
      expect(sessionCookie).toBeFalsy();
    });
  });

  test.describe('Session Management', () => {
    test.use({ storageState: { cookies: [], origins: [] } });

    test('should maintain session across page navigation', async ({ page }) => {
      const loginPage = new LoginPage(page);

      // Login
      await loginPage.login('admin', 'TestPass123!');
      await loginPage.verifyLoginSuccess();

      // Navigate to different pages
      await page.goto('/workspaces');
      await page.waitForLoadState('networkidle');

      await page.goto('/admin');
      await page.waitForLoadState('networkidle');

      await page.goto('/');
      await page.waitForLoadState('networkidle');

      // Should still be authenticated (no login dialog)
      const isLoginDialogVisible = await loginPage.isLoginDialogVisible();
      expect(isLoginDialogVisible).toBeFalsy();
    });

    test('should redirect to login when session expires', async ({ page, context }) => {
      const loginPage = new LoginPage(page);

      // Login
      await loginPage.login('admin', 'TestPass123!');
      await loginPage.verifyLoginSuccess();

      // Clear cookies to simulate session expiry
      await context.clearCookies();

      // Try to navigate to protected page; the app should bounce us back to
      // the login screen once the missing session is detected.
      await page.goto('/admin');
      await page.waitForSelector('#emailOrUsername', { timeout: 10000 });

      const isLoginDialogVisible = await loginPage.isLoginDialogVisible();
      expect(isLoginDialogVisible).toBeTruthy();
    });
  });

  test.describe('Authenticated Actions', () => {
    // These tests use the authenticated state from global setup
    test('should access protected routes when authenticated', async ({ page }) => {
      // Navigate to admin page
      await page.goto('/admin');
      await page.waitForLoadState('networkidle');

      // Should not see login form
      const loginInput = page.locator('#emailOrUsername');
      await expect(loginInput).not.toBeVisible({ timeout: 5000 });

      // Should see admin content (search input as sentinel)
      const adminSearch = page.locator('#admin-search');
      await expect(adminSearch).toBeVisible({ timeout: 5000 });
    });

    test('should display user info when authenticated', async ({ page }) => {
      await page.goto('/');
      await page.waitForLoadState('networkidle');

      // Should see user avatar menu
      const userMenu = page.locator('[data-testid="user-avatar-menu"]');
      await expect(userMenu).toBeVisible({ timeout: 10000 });
    });
  });
});
