/**
 * Test data generators for consistent test data creation
 * Uses timestamps to ensure unique identifiers
 */

export interface TestWorkspace {
  name: string;
  key: string;
  description: string;
}

export interface TestItem {
  title: string;
  description: string;
  workspace_id?: number;
  parent_id?: number;
  status?: string;
  priority?: string;
}

export interface TestUser {
  email: string;
  username: string;
  first_name: string;
  last_name: string;
  password_hash: string;
}

export interface TestGroup {
  name: string;
  description: string;
}

export interface TestTimeProject {
  name: string;
  description: string;
  workspace_id?: number;
}

export interface TestWorklog {
  description: string;
  duration_minutes: number;
  project_id?: number;
  item_id?: number;
  date: string;
}

export interface TestCustomField {
  name: string;
  field_type: string;
  description: string;
  required: boolean;
  options?: string;
}

let workspaceCounter = 0;

/**
 * Generate unique workspace data
 */
export function generateWorkspace(suffix?: string): TestWorkspace {
  const timestamp = Date.now();
  // The name needs same-millisecond entropy too, not just the key: specs
  // resolve the workspace by NAME (WorkspacePage.getWorkspaceId), and two
  // parallel workers minting `E2E Test Workspace <ms>` in the same tick made
  // one of them edit the other's workspace.
  const nonce = Math.random().toString(36).slice(2, 6);
  const uniqueSuffix = suffix || `${timestamp}-${nonce}`;
  // Key max length is 10 characters. Include per-process counter + random
  // entropy so parallel Playwright workers created in the same millisecond
  // don't collide on the workspace key unique constraint.
  const counterPart = (workspaceCounter++ % 36).toString(36).toUpperCase();
  const randomPart = Math.random().toString(36).slice(2, 5).toUpperCase();
  const shortKey = `E${timestamp.toString(36).slice(-5).toUpperCase()}${randomPart}${counterPart}`;

  return {
    name: `E2E Test Workspace ${uniqueSuffix}`,
    key: shortKey,
    description: `Test workspace created by E2E tests at ${new Date().toISOString()}`,
  };
}

/**
 * Generate unique item data
 */
export function generateItem(workspaceId: number, suffix?: string): TestItem {
  const timestamp = Date.now();
  const nonce = Math.random().toString(36).slice(2, 10);
  const uniqueSuffix = suffix ? `${suffix}-${nonce}` : `${timestamp}-${nonce}`;

  return {
    title: `E2E Test Item ${uniqueSuffix}`,
    description: `Test item created by E2E tests at ${new Date().toISOString()}`,
    workspace_id: workspaceId,
    status: 'open',
    priority: 'medium',
  };
}

/**
 * Generate parent-child item structure
 */
export function generateChildItem(
  workspaceId: number,
  parentId: number,
  suffix?: string
): TestItem {
  const item = generateItem(workspaceId, suffix);
  return {
    ...item,
    parent_id: parentId,
    title: `Child ${item.title}`,
  };
}

/**
 * Generate unique user data. Includes a random nonce so parallel Playwright
 * workers that call generateUser() in the same millisecond don't collide on
 * the email/username unique constraints.
 *
 * The server caps username at 32 chars (POST /api/users → 400 above that).
 * We truncate the label portion of the username while keeping the random
 * nonce intact, so callers can pass any descriptive suffix (including ones
 * that already embed a Date.now()) without having to know about the cap.
 * Email and last_name are not constrained the same way and keep the full
 * suffix for debuggability.
 */
export function generateUser(suffix?: string): TestUser {
  const timestamp = Date.now();
  const nonce = Math.random().toString(36).slice(2, 10);
  const uniqueSuffix = suffix ? `${suffix}-${nonce}` : `${timestamp}-${nonce}`;

  const USERNAME_MAX = 32;
  const USERNAME_PREFIX = 'e2euser';
  const noncePortion = `-${nonce}`;
  const labelBudget = USERNAME_MAX - USERNAME_PREFIX.length - noncePortion.length;
  const labelSource = suffix ?? String(timestamp);
  const label = labelSource.slice(0, labelBudget);
  const username = `${USERNAME_PREFIX}${label}${noncePortion}`;

  return {
    email: `e2e.user.${uniqueSuffix}@test.com`,
    username,
    first_name: 'E2E',
    last_name: `User ${uniqueSuffix}`,
    password_hash: 'TestPass123!',
  };
}

/**
 * Generate unique group data (the admin UI calls them "Groups"; the test
 * suite historically called them "Teams").
 */
export function generateGroup(suffix?: string): TestGroup {
  const timestamp = Date.now();
  // Random entropy avoids name collisions across parallel workers in the same
  // millisecond (matches generateWorkspace/generateUser/generateTeam).
  const nonce = Math.random().toString(36).slice(2, 8);
  const uniqueSuffix = suffix || `${timestamp}-${nonce}`;

  return {
    name: `E2E Test Group ${uniqueSuffix}`,
    description: `Test group created by E2E tests at ${new Date().toISOString()}`,
  };
}

/**
 * Generate unique team data (cross-workspace org with on-call).
 */
export function generateTeam(suffix?: string) {
  const timestamp = Date.now();
  // Include random entropy so parallel Playwright workers created in the same
  // millisecond don't collide on the team-name unique constraint (matches
  // generateWorkspace/generateUser).
  const nonce = Math.random().toString(36).slice(2, 8);
  const uniqueSuffix = suffix || `${timestamp}-${nonce}`;
  return {
    name: `E2E Test Team ${uniqueSuffix}`,
    description: `Team for on-call rotations created at ${new Date().toISOString()}`,
  };
}

/**
 * Generate unique on-call schedule data.
 */
export function generateSchedule(suffix?: string) {
  const timestamp = Date.now();
  // Random entropy avoids name collisions across parallel workers in the same
  // millisecond (matches generateWorkspace/generateUser/generateTeam).
  const nonce = Math.random().toString(36).slice(2, 8);
  const uniqueSuffix = suffix || `${timestamp}-${nonce}`;
  return {
    name: `E2E Schedule ${uniqueSuffix}`,
    description: `Schedule for ${uniqueSuffix}`,
    timezone: 'UTC',
  };
}

/**
 * Generate unique time project data
 */
export function generateTimeProject(suffix?: string): TestTimeProject {
  const timestamp = Date.now();
  const nonce = Math.random().toString(36).slice(2, 8);
  const uniqueSuffix = [suffix, timestamp, nonce].filter(Boolean).join('-');

  return {
    name: `E2E Time Project ${uniqueSuffix}`,
    description: `Test time project created at ${new Date().toISOString()}`,
  };
}

/**
 * Generate unique worklog data
 */
export function generateWorklog(suffix?: string): TestWorklog {
  const timestamp = Date.now();
  const nonce = Math.random().toString(36).slice(2, 8);
  const uniqueSuffix = [suffix, timestamp, nonce].filter(Boolean).join('-');

  return {
    description: `E2E Worklog ${uniqueSuffix}`,
    duration_minutes: 60,
    date: new Date().toISOString().split('T')[0],
  };
}

/**
 * Generate custom field data for any of the 13 supported field types.
 * Select/multiselect types include default options.
 */
export function generateCustomField(type: string = 'text', suffix?: string): TestCustomField {
  const timestamp = Date.now();
  const uniqueSuffix = suffix || `${timestamp}`;

  const baseField: TestCustomField = {
    name: `E2E ${type} Field ${uniqueSuffix}`,
    field_type: type,
    description: `Test ${type} field created at ${new Date().toISOString()}`,
    required: false,
  };

  // Add options for select/multiselect fields
  if (type === 'select' || type === 'multiselect') {
    baseField.options = JSON.stringify(['Option 1', 'Option 2', 'Option 3']);
  }

  return baseField;
}

/**
 * Generate workflow data
 */
export function generateWorkflow(suffix?: string) {
  const timestamp = Date.now();
  const uniqueSuffix = suffix || `${timestamp}`;

  return {
    name: `E2E Test Workflow ${uniqueSuffix}`,
    description: `Test workflow created at ${new Date().toISOString()}`,
  };
}

/**
 * Generate status category data
 */
export function generateStatusCategory(suffix?: string) {
  const timestamp = Date.now();
  const uniqueSuffix = suffix || `${timestamp}`;

  return {
    name: `E2E Status Category ${uniqueSuffix}`,
    color: '#3b82f6',
    description: `Test status category created at ${new Date().toISOString()}`,
    is_default: false,
  };
}

/**
 * Generate status data
 */
export function generateStatus(categoryId: number, suffix?: string) {
  const timestamp = Date.now();
  const uniqueSuffix = suffix || `${timestamp}`;

  return {
    name: `E2E Status ${uniqueSuffix}`,
    description: `Test status created at ${new Date().toISOString()}`,
    category_id: categoryId,
    is_default: false,
  };
}

/**
 * Generate screen data
 */
export function generateScreen(suffix?: string) {
  const timestamp = Date.now();
  const uniqueSuffix = suffix || `${timestamp}`;

  return {
    name: `E2E Test Screen ${uniqueSuffix}`,
    description: `Test screen created at ${new Date().toISOString()}`,
  };
}

/**
 * Generate configuration set data
 */
export function generateConfigurationSet(
  workflowId?: number,
  createScreenId?: number,
  editScreenId?: number,
  suffix?: string
) {
  const timestamp = Date.now();
  const uniqueSuffix = suffix || `${timestamp}`;

  return {
    name: `E2E Config Set ${uniqueSuffix}`,
    description: `Test configuration set created at ${new Date().toISOString()}`,
    workflow_id: workflowId,
    create_screen_id: createScreenId,
    edit_screen_id: editScreenId,
    is_default: false,
  };
}

export interface TestIteration {
  name: string;
  description: string;
  start_date: string;
  end_date: string;
  status: 'planned' | 'active' | 'completed' | 'cancelled';
}

/**
 * Generate unique iteration data. Dates span the next two weeks by default so
 * the `isActive()` / `isOverdue()` helpers in the UI behave deterministically
 * — tests that need different timing can override start_date / end_date.
 */
export function generateIteration(suffix?: string): TestIteration {
  const timestamp = Date.now();
  const nonce = Math.random().toString(36).slice(2, 10);
  const uniqueSuffix = suffix ? `${suffix}-${nonce}` : `${timestamp}-${nonce}`;
  const start = new Date();
  const end = new Date();
  end.setDate(end.getDate() + 14);
  return {
    name: `E2E Iteration ${uniqueSuffix}`,
    description: `Test iteration created at ${new Date().toISOString()}`,
    start_date: start.toISOString().split('T')[0],
    end_date: end.toISOString().split('T')[0],
    status: 'planned',
  };
}

export interface TestMilestone {
  name: string;
  description: string;
  target_date: string;
  status: 'planning' | 'in-progress' | 'completed' | 'cancelled';
}

/**
 * Generate unique milestone data. Target date defaults to +30 days.
 */
export function generateMilestone(suffix?: string): TestMilestone {
  const timestamp = Date.now();
  const nonce = Math.random().toString(36).slice(2, 10);
  const uniqueSuffix = suffix ? `${suffix}-${nonce}` : `${timestamp}-${nonce}`;
  const target = new Date();
  target.setDate(target.getDate() + 30);
  return {
    name: `E2E Milestone ${uniqueSuffix}`,
    description: `Test milestone created at ${new Date().toISOString()}`,
    target_date: target.toISOString().split('T')[0],
    status: 'planning',
  };
}

export interface TestCollection {
  name: string;
  description: string;
}

/**
 * Generate unique collection data. The QL filter (`ql_query`) is passed
 * straight through to the API at call-time since it's scenario-specific
 * (e.g. `iteration_id = 5`).
 */
export function generateCollection(suffix?: string): TestCollection {
  const timestamp = Date.now();
  const nonce = Math.random().toString(36).slice(2, 10);
  const uniqueSuffix = suffix ? `${suffix}-${nonce}` : `${timestamp}-${nonce}`;
  return {
    name: `E2E Collection ${uniqueSuffix}`,
    description: `Test collection created at ${new Date().toISOString()}`,
  };
}

export interface TestPriority {
  name: string;
  description: string;
  icon: string;
  color: string;
  sort_order: number;
  is_default: boolean;
}

/**
 * Generate unique priority data
 */
export function generatePriority(suffix?: string): TestPriority {
  const timestamp = Date.now();
  const uniqueSuffix = suffix || `${timestamp}`;
  return {
    name: `E2E Priority ${uniqueSuffix}`,
    description: `Test priority created at ${new Date().toISOString()}`,
    icon: 'AlertCircle',
    color: '#7c3aed',
    sort_order: 99,
    is_default: false,
  };
}

/**
 * Wait helper for animations or async operations
 */
export async function waitFor(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Generate random string of specified length
 */
export function randomString(length: number = 10): string {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  let result = '';
  for (let i = 0; i < length; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return result;
}
