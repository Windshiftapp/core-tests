import {
  authenticateAdminRequest,
  createItemViaAPI,
  createUserViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test, type APIRequestContext } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * Mention hover cards must show the person the mention actually refers to.
 * Usernames are unique and resolve directly; a display name that several
 * people share lists every candidate instead of guessing.
 */
async function assignEditorRole(
  request: APIRequestContext,
  userId: number,
  workspaceId: number,
): Promise<void> {
  const rolesResp = await request.get('/api/workspace-roles', { headers: SEC_FETCH });
  const rolesBody = await rolesResp.json();
  const roles: Array<{ id: number; name: string }> = rolesBody.data ?? rolesBody;
  const editorRole = roles.find((role) => role.name === 'Editor');
  if (!editorRole) throw new Error('Editor role missing from seed');

  const resp = await request.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: { user_id: userId, workspace_id: workspaceId, role_id: editorRole.id },
  });
  expect(resp.ok(), `assign Editor role: ${resp.status()} ${await resp.text()}`).toBeTruthy();
}

test.describe('Mention hover card', () => {
  test('resolves a username and lists candidates for an ambiguous display name', async ({
    page,
    request,
  }, testInfo) => {
    test.setTimeout(60_000);
    await authenticateAdminRequest(request);

    const suffix = `hc${Date.now()}w${testInfo.workerIndex}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));

    // Two workspace members share a display name but keep distinct usernames.
    const createMember = async (handle: string) => {
      const user = await createUserViaAPI(request, {
        email: `${handle}.${suffix}@example.com`,
        username: `${handle}-${suffix}`.slice(0, 32),
        first_name: 'John',
        last_name: 'Doe',
        password_hash: 'TestPass123!',
      });
      await assignEditorRole(request, user.id, ws.id);
      return user;
    };
    const first = await createMember('jdoe');
    const second = await createMember('john.doe');

    const item = await createItemViaAPI(request, ws.id, {
      title: `Mention hover ${suffix}`,
      description: `Ping @${first.username} and @"John Doe" please`,
    });

    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
    const display = page.getByTestId('item-description-display');
    await expect(display).toBeVisible({ timeout: 15_000 });

    // A username mention resolves to exactly one person.
    const usernameChip = page.getByTestId(`mention-chip-${first.username}`);
    await expect(usernameChip).toBeVisible({ timeout: 15_000 });
    await usernameChip.hover();

    const card = page.getByTestId('mention-hover-card');
    await expect(card).toBeVisible({ timeout: 10_000 });
    const userCard = card.getByTestId('mention-hover-card-user');
    await expect(userCard).toContainText('John Doe');
    await expect(userCard).toContainText(`@${first.username}`);

    // A display-name mention that matches two people lists both candidates.
    const ambiguousChip = page.getByTestId('mention-chip-John Doe');
    await expect(ambiguousChip).toBeVisible({ timeout: 10_000 });
    await ambiguousChip.hover();

    const candidates = page.getByTestId('mention-hover-card-candidates');
    await expect(candidates).toBeVisible({ timeout: 10_000 });
    await expect(
      candidates.getByTestId(`mention-hover-card-candidate-${first.id}`),
    ).toContainText(`@${first.username}`);
    await expect(
      candidates.getByTestId(`mention-hover-card-candidate-${second.id}`),
    ).toContainText(`@${second.username}`);
  });
});
