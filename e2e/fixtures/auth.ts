import { type APIRequestContext, test as base, expect, type Page } from './context-path';

/**
 * Authentication fixtures for Playwright tests
 * Provides reusable authentication helpers and authenticated contexts
 */

export interface AuthFixtures {
  /**
   * Performs login via UI
   */
  loginViaUI: (page: Page, username: string, password: string) => Promise<void>;

  /**
   * Performs logout via UI
   */
  logoutViaUI: (page: Page) => Promise<void>;

  /**
   * Creates a bearer token for v1 API authentication. The returned token
   * is usable only against /rest/api/v1/* — pair it with makeBearerRequest.
   * Cookie-auth (/api/*) does not accept bearer tokens; for /api/* calls
   * use makeAuthRequest which relies on the request context's session
   * cookie established by loginViaUI.
   */
  createBearerToken: (request: APIRequestContext, name: string) => Promise<string>;

  /**
   * Makes a session-authenticated request to /api/<endpoint>. The request
   * context's cookies (set by loginViaUI) carry the session — no explicit
   * auth header needed.
   */
  makeAuthRequest: (
    request: APIRequestContext,
    method: string,
    endpoint: string,
    data?: any
  ) => Promise<any>;

  /**
   * Makes a bearer-authenticated request to a v1 path (full path including
   * the /rest/api/v1 prefix).
   */
  makeBearerRequest: (
    request: APIRequestContext,
    token: string,
    method: string,
    path: string,
    data?: any
  ) => Promise<any>;
}

export const test = base.extend<AuthFixtures>({
  /**
   * Login via UI (for testing login functionality)
   */
  loginViaUI: async ({ page }, use) => {
    const login = async (pageContext: Page, username: string, password: string): Promise<void> => {
      const baseURL = process.env.BASE_URL || 'http://localhost:8080';

      // Navigate to app (login dialog should appear)
      await pageContext.goto(baseURL);

      // Wait for login form
      await pageContext.waitForSelector('#emailOrUsername', { timeout: 10000 });

      // Fill credentials
      await pageContext.locator('#emailOrUsername').fill(username);
      await pageContext.locator('#password').fill(password);

      // Submit
      const loginResponse = pageContext
        .waitForResponse((res) => res.url().includes('/api/auth/login'), { timeout: 10000 })
        .catch(() => null);
      await pageContext.click('button[type="submit"]');
      await loginResponse;
      await pageContext
        .locator('#emailOrUsername')
        .waitFor({ state: 'detached', timeout: 10000 })
        .catch(() => {});

      // Verify login success
      const cookies = await pageContext.context().cookies();
      const hasSession = cookies.some(
        (c) => c.name === 'session' || c.name === 'windshift_session'
      );
      expect(hasSession).toBeTruthy();
    };
    await use(login);
  },

  /**
   * Logout via UI
   */
  logoutViaUI: async ({ page }, use) => {
    const logout = async (pageContext: Page): Promise<void> => {
      // Click user menu/avatar
      await pageContext.click('[data-testid="user-menu"], .user-avatar, button:has-text("admin")');

      // Wait for dropdown menu
      await pageContext.waitForSelector('text=Logout, text=Sign out', { timeout: 5000 });

      // Click logout
      await pageContext.click('text=Logout, text=Sign out');

      // Verify we're logged out (login dialog should appear or redirected)
      await pageContext.waitForSelector('#emailOrUsername', { timeout: 10000 });
    };
    await use(logout);
  },

  /**
   * Create bearer token for API authentication
   */
  createBearerToken: async ({ request }, use) => {
    const createToken = async (
      requestContext: APIRequestContext,
      name: string
    ): Promise<string> => {
      const baseURL = process.env.BASE_URL || 'http://localhost:8080';

      // Create token (Sec-Fetch-Site header needed for programmatic requests)
      const tokenResponse = await requestContext.post(`${baseURL}/api/api-tokens`, {
        headers: {
          'Sec-Fetch-Site': 'same-origin',
        },
        data: {
          name: name,
          permissions: ['read', 'write', 'admin'],
        },
      });

      expect(tokenResponse.ok()).toBeTruthy();
      const tokenData = await tokenResponse.json();
      return tokenData.token;
    };
    await use(createToken);
  },

  /**
   * Make a session-authenticated request to /api/<endpoint>. Auth comes
   * from the request context's session cookie (established by loginViaUI
   * or by an earlier call that minted a session). The cookie-auth surface
   * does not accept bearer tokens — for v1 traffic use makeBearerRequest.
   */
  makeAuthRequest: async ({ request }, use) => {
    const makeRequest = async (
      requestContext: APIRequestContext,
      method: string,
      endpoint: string,
      data?: any
    ): Promise<any> => {
      const baseURL = process.env.BASE_URL || 'http://localhost:8080';
      const url = `${baseURL}/api${endpoint}`;

      const options: any = {};
      if (data) {
        options.data = data;
      }

      let response;
      switch (method.toUpperCase()) {
        case 'GET':
          response = await requestContext.get(url, options);
          break;
        case 'POST':
          response = await requestContext.post(url, options);
          break;
        case 'PUT':
          response = await requestContext.put(url, options);
          break;
        case 'DELETE':
          response = await requestContext.delete(url, options);
          break;
        default:
          throw new Error(`Unsupported HTTP method: ${method}`);
      }

      if (!response.ok()) {
        const body = await response.text();
        console.error(`API request failed: ${method} ${endpoint}`, body);
      }

      return response;
    };
    await use(makeRequest);
  },

  /**
   * Make a bearer-authenticated request to a v1 path (full path including
   * the /rest/api/v1 prefix). Pair with createBearerToken.
   */
  makeBearerRequest: async ({ request }, use) => {
    const makeRequest = async (
      requestContext: APIRequestContext,
      token: string,
      method: string,
      path: string,
      data?: any
    ): Promise<any> => {
      const baseURL = process.env.BASE_URL || 'http://localhost:8080';
      const url = `${baseURL}${path}`;

      const options: any = {
        headers: {
          Authorization: `Bearer ${token}`,
        },
      };
      if (data) {
        options.data = data;
      }

      let response;
      switch (method.toUpperCase()) {
        case 'GET':
          response = await requestContext.get(url, options);
          break;
        case 'POST':
          response = await requestContext.post(url, options);
          break;
        case 'PUT':
          response = await requestContext.put(url, options);
          break;
        case 'DELETE':
          response = await requestContext.delete(url, options);
          break;
        default:
          throw new Error(`Unsupported HTTP method: ${method}`);
      }

      if (!response.ok()) {
        const body = await response.text();
        console.error(`Bearer request failed: ${method} ${path}`, body);
      }

      return response;
    };
    await use(makeRequest);
  },
});

export { expect } from './context-path';
