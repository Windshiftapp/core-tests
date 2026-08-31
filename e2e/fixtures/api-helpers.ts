import { type APIRequestContext, expect } from './context-path';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

const defaultHeaders = {
  'Sec-Fetch-Site': 'same-origin',
};

/** Authenticate a standalone setup client with its own IP-bound session. */
export async function authenticateAdminRequest(request: APIRequestContext): Promise<void> {
  const response = await request.post(`${BASE_URL}/api/auth/login`, {
    data: {
      email_or_username: 'admin',
      password: 'TestPass123!',
      remember_me: false,
    },
  });
  expect(
    response.ok(),
    `admin API login failed (${response.status()}): ${await response.text()}`
  ).toBeTruthy();
}

/**
 * Create a workspace via the API (bypasses UI for faster test setup)
 */
export async function createWorkspaceViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    key: string;
    description: string;
    time_project_id?: number;
  }
) {
  const response = await request.post(`${BASE_URL}/api/workspaces`, {
    headers: defaultHeaders,
    data,
  });
  expect(
    response.ok(),
    `create workspace failed (${response.status()}): ${await response.text()}`
  ).toBeTruthy();
  return response.json();
}

/**
 * List item types via the API (global catalog). Returns the raw array.
 */
export async function listItemTypesViaAPI(request: APIRequestContext) {
  const response = await request.get(`${BASE_URL}/api/item-types`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  return Array.isArray(body) ? body : (body.data ?? body.items ?? []);
}

/**
 * Create a work item template via the cookie-auth admin API (WI-438).
 */
export async function createTemplateViaAPI(
  request: APIRequestContext,
  data: {
    workspace_id: number;
    name: string;
    description_body: string;
    mode?: 'selectable' | 'mandatory';
    is_active?: boolean;
    item_type_ids?: number[];
  }
) {
  const response = await request.post(`${BASE_URL}/api/item-templates`, {
    headers: defaultHeaders,
    data: { mode: 'selectable', is_active: true, item_type_ids: [], ...data },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Create an item via the API (bypasses UI for faster test setup)
 */
export async function createItemViaAPI(
  request: APIRequestContext,
  workspaceId: number,
  data: {
    title: string;
    description?: string;
    status?: string;
    priority?: string;
    assignee_id?: number;
    parent_id?: number;
    start_date?: string;
    end_date?: string;
    custom_field_values?: Record<string, unknown>;
  }
) {
  const response = await request.post(`${BASE_URL}/api/items`, {
    headers: defaultHeaders,
    data: { ...data, workspace_id: workspaceId },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Create a user via the API (bypasses UI for faster test setup).
 *
 * The `password_hash` field carries the plaintext password — the backend
 * (CreateUserRequest in handlers/users.go) hashes it server-side under the
 * `password` JSON key, so we remap here. Without this remap the user is
 * created with a NULL password_hash and cannot log in.
 *
 * Since core commit 73b2b39 the backend hard-codes is_active=false on
 * create (defense-in-depth against mass-assigned activation). So tests that
 * want a login-ready user have to follow up with POST /users/{id}/activate.
 * This helper does that automatically unless `is_active: false` is requested.
 */
export async function createUserViaAPI(
  request: APIRequestContext,
  data: {
    email: string;
    username: string;
    first_name: string;
    last_name: string;
    password_hash: string;
    is_active?: boolean;
  }
) {
  const { password_hash, is_active = true, ...rest } = data;
  const response = await request.post(`${BASE_URL}/api/users`, {
    headers: defaultHeaders,
    data: {
      ...rest,
      password: password_hash,
    },
  });
  expect(response.ok()).toBeTruthy();
  const user = await response.json();
  if (is_active) {
    const activateResp = await request.post(`${BASE_URL}/api/users/${user.id}/activate`, {
      headers: defaultHeaders,
    });
    expect(
      activateResp.ok(),
      `activate user ${user.id} failed (status ${activateResp.status()})`
    ).toBeTruthy();
  }
  return user;
}

/**
 * Create a team via the API (bypasses UI for faster test setup)
 */
export async function createTeamViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    description?: string;
    member_ids?: number[];
  }
) {
  const response = await request.post(`${BASE_URL}/api/teams`, {
    headers: defaultHeaders,
    data,
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Create a group via the API (bypasses UI for faster test setup)
 */
export async function createGroupViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    description?: string;
  }
) {
  const response = await request.post(`${BASE_URL}/api/groups`, {
    headers: defaultHeaders,
    data,
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * List all custom fields via the API
 */
export async function listCustomFieldsViaAPI(request: APIRequestContext) {
  const response = await request.get(`${BASE_URL}/api/custom-fields`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  return body.data || body;
}

/**
 * Create a custom field via the API (bypasses UI for faster test setup)
 */
export async function createCustomFieldViaAPI(
  request: APIRequestContext,
  data: {
    name?: string;
    field_name?: string;
    field_type: string;
    description?: string;
    required?: boolean;
    options?: string;
    field_config?: Record<string, unknown>;
  }
) {
  const { field_name, ...rest } = data;
  const response = await request.post(`${BASE_URL}/api/admin/custom-fields`, {
    headers: defaultHeaders,
    data: {
      ...rest,
      name: data.name ?? field_name,
    },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Delete a custom field via the API
 */
export async function deleteCustomFieldViaAPI(request: APIRequestContext, fieldId: number) {
  const response = await request.delete(`${BASE_URL}/api/admin/custom-fields/${fieldId}`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
}

/**
 * List all priorities via the API
 */
export async function listPrioritiesViaAPI(request: APIRequestContext) {
  const response = await request.get(`${BASE_URL}/api/priorities`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Create a priority via the API (bypasses UI for faster test setup)
 */
export async function createPriorityViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    description?: string;
    icon?: string;
    color?: string;
    sort_order?: number;
    is_default?: boolean;
  }
) {
  const response = await request.post(`${BASE_URL}/api/priorities`, {
    headers: defaultHeaders,
    data,
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Delete a priority via the API
 */
export async function deletePriorityViaAPI(request: APIRequestContext, priorityId: number) {
  const response = await request.delete(`${BASE_URL}/api/priorities/${priorityId}`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
}

/**
 * Create a customer organisation via the API
 */
export async function createCustomerOrgViaAPI(
  request: APIRequestContext,
  data: { name: string; email?: string; active?: boolean }
) {
  const response = await request.post(`${BASE_URL}/api/customer-organisations`, {
    headers: defaultHeaders,
    data: { ...data, active: data.active ?? true },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Create a time project via the API
 */
export async function createTimeProjectViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    customer_id: number;
    status?: string;
    description?: string;
  }
) {
  const response = await request.post(`${BASE_URL}/api/time/projects`, {
    headers: defaultHeaders,
    data: { ...data, status: data.status ?? 'Active' },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * List link types. The server seeds seven system link types on init:
 * Tests (id 1), Implements (2), Depends On (3), Relates To (4), Links To (5),
 * Duplicates (6), Child Of (7).
 */
export async function listLinkTypesViaAPI(request: APIRequestContext) {
  const response = await request.get(`${BASE_URL}/api/link-types`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  return (body.data ?? body) as Array<{
    id: number;
    name: string;
    active?: boolean;
  }>;
}

/**
 * Create a link between two entities via the API. Accepts the same payload
 * shape as the handler: link_type_id + source/target type+id. Returns the
 * created link record.
 */
export async function createLinkViaAPI(
  request: APIRequestContext,
  data: {
    link_type_id: number;
    source_type: 'item' | 'test_case' | 'asset';
    source_id: number;
    target_type: 'item' | 'test_case' | 'asset';
    target_id: number;
  }
) {
  const response = await request.post(`${BASE_URL}/api/links`, {
    headers: defaultHeaders,
    data,
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * List outgoing and incoming links for an item. Returns
 * { outgoing: [...], incoming: [...] }.
 */
export async function listLinksForItemViaAPI(request: APIRequestContext, itemId: number) {
  const response = await request.get(`${BASE_URL}/api/items/${itemId}/links`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Delete a link by id.
 */
export async function deleteLinkViaAPI(request: APIRequestContext, linkId: number) {
  const response = await request.delete(`${BASE_URL}/api/links/${linkId}`, {
    headers: defaultHeaders,
  });
  expect(response.status()).toBe(204);
}

/**
 * List iteration types. The server seeds "Sprint" by default — any newly
 * created iteration needs a type_id, so tests fetch the list and pick one.
 */
export async function listIterationTypesViaAPI(request: APIRequestContext) {
  const response = await request.get(`${BASE_URL}/api/iteration-types`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  return (body.data ?? body) as Array<{ id: number; name: string }>;
}

/**
 * Create an iteration via the API. Pass `workspace_id` for a workspace-scoped
 * iteration, or `is_global: true` for a global one (with `workspace_id: null`).
 */
export async function createIterationViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    start_date: string;
    end_date: string;
    type_id: number;
    workspace_id: number | null;
    is_global?: boolean;
    status?: 'planned' | 'active' | 'completed' | 'cancelled';
    description?: string;
  }
) {
  const response = await request.post(`${BASE_URL}/api/iterations`, {
    headers: defaultHeaders,
    data: {
      ...data,
      status: data.status ?? 'planned',
      is_global: data.is_global ?? data.workspace_id === null,
    },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * List milestone categories. Tests can pick the first (or filter by name) to
 * get a stable `category_id` for milestone creation.
 */
export async function listMilestoneCategoriesViaAPI(request: APIRequestContext) {
  const response = await request.get(`${BASE_URL}/api/milestone-categories`, {
    headers: defaultHeaders,
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  return (body.data ?? body) as Array<{
    id: number;
    name: string;
    color?: string;
  }>;
}

/**
 * Create a milestone via the API. Same workspace/global pattern as
 * createIterationViaAPI — `workspace_id` for workspace-scoped, `is_global:true`
 * + `workspace_id: null` for global.
 */
export async function createMilestoneViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    workspace_id: number | null;
    is_global?: boolean;
    status?: 'planning' | 'in-progress' | 'completed' | 'cancelled';
    target_date?: string;
    category_id?: number | null;
    description?: string;
  }
) {
  const response = await request.post(`${BASE_URL}/api/milestones`, {
    headers: defaultHeaders,
    data: {
      ...data,
      status: data.status ?? 'planning',
      is_global: data.is_global ?? data.workspace_id === null,
    },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Update a single item field. Used by iteration/milestone specs to assign
 * `iteration_id`/`milestone_id` without clicking through the sidebar picker
 * when the UI path is already covered by a dedicated test.
 */
export async function updateItemViaAPI(
  request: APIRequestContext,
  itemId: number,
  data: Record<string, unknown>
) {
  const response = await request.put(`${BASE_URL}/api/items/${itemId}`, {
    headers: defaultHeaders,
    data,
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}

/**
 * Create a collection via the API. Pass `ql_query` for a raw CQL filter
 * (e.g. `iteration_id = 5`) — the backend evaluates it against the items
 * table. `workspace_id` restricts the collection to one workspace.
 */
export async function createCollectionViaAPI(
  request: APIRequestContext,
  data: {
    name: string;
    ql_query?: string;
    description?: string;
    workspace_id?: number | null;
    is_public?: boolean;
  }
) {
  const response = await request.post(`${BASE_URL}/api/collections`, {
    headers: defaultHeaders,
    data: {
      ...data,
      is_public: data.is_public ?? false,
    },
  });
  expect(response.ok()).toBeTruthy();
  return response.json();
}
