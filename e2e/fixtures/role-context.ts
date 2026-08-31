import { createUserViaAPI, createWorkspaceViaAPI } from './api-helpers';
import { type APIRequestContext, test as base, expect } from './context-path';
import { generateUser, generateWorkspace } from './test-data';

/**
 * Roleful test fixture.
 *
 * Most existing specs run as the global-admin user (system_admin bypasses
 * every permission check via permission_cache.go HasWorkspacePermission), so
 * the suite never exercises the non-admin code paths. This fixture provides
 * a uniform way to get an authenticated context for either role:
 *
 *   const ctx = await getCtx('admin');   // → worker-scoped admin context
 *   const ctx = await getCtx('member');  // → worker-scoped fresh user, Editor on a fresh workspace
 *
 * Tests can iterate over `ROLES` to run the same body in both contexts:
 *
 *   for (const role of ROLES) {
 *     test(`[${role}] create item`, async ({ getCtx }) => {
 *       const ctx = await getCtx(role);
 *       await createItemViaAPI(ctx.request, ctx.workspaceId, { ... });
 *     });
 *   }
 *
 * Scope:
 * - The fixture is **worker-scoped** so all tests in a run share the same
 *   admin and member contexts. This keeps logins under the server's
 *   loginRateLimiter budget (5/min, burst 10 — see server.go:235), which
 *   would otherwise lock out a fresh-per-test member.
 * - All tests therefore share one workspace per role per `RoleOptions` shape.
 *   Assertions should look up state by ID/name, not by counts, since data
 *   from earlier tests persists in the workspace.
 *
 * Member role:
 * - Editor by default — covers `item.view` + `item.comment` + `item.edit`,
 *   but **NOT** `item.delete` (that's Administrator-only per
 *   permissions.sql:230-276). Tests that exercise delete should assert the
 *   member-deny path returns 404.
 * - `getCtx('member', { isolated: true })` additionally assigns Viewer,
 *   restricting the everyone-Viewer fallback (permission_cache.go:1004-1008)
 *   and fully gating the workspace.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

export type Role = 'admin' | 'member';
export const ROLES: readonly Role[] = ['admin', 'member'] as const;

export interface RoleContext {
  role: Role;
  /** Authenticated API context. Worker-scoped — same instance across tests. */
  request: APIRequestContext;
  userId: number;
  username: string;
  /** User's email address — needed for email-bearing flows (mailpit assertions). */
  email: string;
  /** Workspace where the role has access. For member, gated via Editor role assignment. */
  workspaceId: number;
}

export interface RoleOptions {
  /**
   * If true, also assign the seeded `Viewer` role to the member, which
   * restricts Viewer-everyone and fully gates the workspace (no implicit
   * `item.view` for non-members). Default false — most tests only care about
   * the happy path inside the member's workspace, not external isolation.
   */
  isolated?: boolean;
}

async function loginAs(ctx: APIRequestContext, username: string, password: string): Promise<void> {
  const resp = await ctx.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: { email_or_username: username, password, remember_me: false },
  });
  expect(resp.ok(), `login as ${username} failed (status ${resp.status()})`).toBeTruthy();
}

async function getRoleIdByName(ctx: APIRequestContext, name: string): Promise<number> {
  const resp = await ctx.get('/api/workspace-roles', { headers: SEC_FETCH });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  const roles: Array<{ id: number; name: string }> = body.data ?? body;
  const role = roles.find((r) => r.name === name);
  if (!role) throw new Error(`workspace role "${name}" not found in seeded roles`);
  return role.id;
}

async function assignWorkspaceRole(
  ctx: APIRequestContext,
  userId: number,
  workspaceId: number,
  roleId: number
): Promise<void> {
  const resp = await ctx.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: { user_id: userId, workspace_id: workspaceId, role_id: roleId },
  });
  expect(resp.ok(), `role assign failed (status ${resp.status()})`).toBeTruthy();
}

async function whoAmI(
  ctx: APIRequestContext
): Promise<{ id: number; username: string; email: string }> {
  const resp = await ctx.get('/api/auth/me', { headers: SEC_FETCH });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  return {
    id: body.user.id,
    username: body.user.username,
    email: body.user.email ?? '',
  };
}

interface RoleFixtures {
  /**
   * Get an authenticated context for the given role. Worker-scoped — same
   * context returned for the entire test run.
   */
  getCtx: (role: Role, opts?: RoleOptions) => Promise<RoleContext>;
}

interface WorkerFixtures {
  /** Worker-scoped admin API context, built from the global-setup auth file. */
  adminCtx: APIRequestContext;
}

export const test = base.extend<RoleFixtures, WorkerFixtures>({
  adminCtx: [
    async ({ playwright }, use) => {
      // The global setup writes a session cookie to E2E_AUTH_FILE
      // (defaults to .auth/user.json — see playwright.config.ts).
      // Build a worker-scoped APIRequestContext that reuses it.
      const storageState = process.env.E2E_AUTH_FILE || '.auth/user.json';
      const ctx = await playwright.request.newContext({
        baseURL: BASE_URL,
        storageState,
      });
      await use(ctx);
      await ctx.dispose().catch(() => {});
    },
    { scope: 'worker' },
  ],

  getCtx: [
    async ({ adminCtx, playwright }, use) => {
      const cache = new Map<string, RoleContext>();
      const ownedContexts: APIRequestContext[] = [];

      const cacheKey = (role: Role, opts?: RoleOptions): string =>
        `${role}:${opts?.isolated ? 'iso' : 'open'}`;

      await use(async (role, opts) => {
        const key = cacheKey(role, opts);
        const cached = cache.get(key);
        if (cached) return cached;

        let ctx: RoleContext;
        const stamp = `${role}-${Date.now()}`;

        if (role === 'admin') {
          // Admin = global-setup user (system_admin) — bypasses every gate.
          const me = await whoAmI(adminCtx);
          const wsData = generateWorkspace(stamp);
          const ws = await createWorkspaceViaAPI(adminCtx, wsData);
          ctx = {
            role,
            request: adminCtx,
            userId: me.id,
            username: me.username,
            email: me.email,
            workspaceId: ws.id,
          };
        } else {
          // Member: admin creates user + workspace, assigns Editor (and Viewer
          // when isolated). One login per worker thanks to the cache below.
          const userData = generateUser(stamp);
          const user = await createUserViaAPI(adminCtx, userData);
          const wsData = generateWorkspace(stamp);
          const ws = await createWorkspaceViaAPI(adminCtx, wsData);

          const editorRoleId = await getRoleIdByName(adminCtx, 'Editor');
          await assignWorkspaceRole(adminCtx, user.id, ws.id, editorRoleId);

          if (opts?.isolated) {
            const viewerRoleId = await getRoleIdByName(adminCtx, 'Viewer');
            await assignWorkspaceRole(adminCtx, user.id, ws.id, viewerRoleId);
          }

          const memberCtx = await playwright.request.newContext({ baseURL: BASE_URL });
          ownedContexts.push(memberCtx);
          await loginAs(memberCtx, userData.username, userData.password_hash);

          ctx = {
            role,
            request: memberCtx,
            userId: user.id,
            username: userData.username,
            email: userData.email,
            workspaceId: ws.id,
          };
        }

        cache.set(key, ctx);
        return ctx;
      });

      for (const c of ownedContexts) {
        await c.dispose().catch(() => {});
      }
    },
    { scope: 'worker' },
  ],
});

export { expect };
