import { expect, logicalPath, test } from '../fixtures/context-path';
import {
	createItemViaAPI,
	createLinkViaAPI,
	createWorkspaceViaAPI,
	listLinkTypesViaAPI,
	listLinksForItemViaAPI,
} from '../fixtures/api-helpers';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { ItemLinksPage } from '../pages/item-links.page';
import { ItemPage } from '../pages/item.page';

// Item ↔ test case linking through the LinkItemModal UI. The "Tests" link
// type is the only seeded type that allows test_case targets, and the link
// modal's search scopes results to the selected type's allowed entity types.
// The work-item side is browser-asserted; the test-case side has no links
// view in the UI, so its /test-cases/{id}/links endpoint is asserted
// directly as the reverse-surface contract.

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('Item ↔ test case linking', () => {
	let testsLinkTypeId: number;
	let workspaceId: string;
	let workspaceNumericId: number;
	let item: { id: number; title: string };
	let testCase: { id: number; title: string };
	let linksPage: ItemLinksPage;
	let itemPage: ItemPage;

	test.beforeAll(async ({ request }) => {
		const linkTypes = await listLinkTypesViaAPI(request);
		const testsType = linkTypes.find((linkType) => linkType.name === 'Tests');
		if (!testsType) {
			throw new Error('Default "Tests" link type was not seeded');
		}
		testsLinkTypeId = testsType.id;
	});

	test.beforeEach(async ({ page, request }) => {
		linksPage = new ItemLinksPage(page);
		itemPage = new ItemPage(page);

		const stamp = `tcl-${Date.now().toString(36)}`;
		const testWorkspace = generateWorkspace();
		const workspace = await createWorkspaceViaAPI(request, {
			name: testWorkspace.name,
			key: testWorkspace.key,
			description: testWorkspace.description,
		});
		workspaceNumericId = workspace.id;
		workspaceId = String(workspace.id);

		const source = generateItem(0, 'src');
		item = { id: (await createItemViaAPI(request, workspaceNumericId, { title: source.title })).id, title: source.title };

			testCase = await createTestCaseViaAPI(request, workspaceNumericId, `${stamp} checkout flow`);
	});

	async function createTestCaseViaAPI(
		request: Parameters<typeof createItemViaAPI>[0],
		workspaceNumericId: number,
		title: string
	): Promise<{ id: number; title: string }> {
		const response = await request.post(`/api/v2/workspaces/${workspaceNumericId}/test-cases`, {
			headers: SEC_FETCH,
			data: { title, preconditions: `preconditions for ${title}`, priority: 'medium', status: 'active' },
		});
		expect(response.status(), await response.text()).toBe(201);
		const body = (await response.json()) as { data: { id: number; title: string } };
		return { id: body.data.id, title: body.data.title };
	}

	test('links a test case via the modal and renders it in the detail view', async ({
		request,
	}) => {
		await itemPage.gotoWorkspaceBacklog(workspaceId);
		await itemPage.openItemDetailModal(item.title);

		await linksPage.openLinkModal();
		await linksPage.selectLinkType('Tests');
		await linksPage.searchAndSelect(testCase.title);
		await linksPage.submitLink();

		await linksPage.expectLinkVisible(testCase.title);

		// Work-item side: exactly one outgoing link, typed as test_case.
		const links = await listLinksForItemViaAPI(request, item.id);
		expect(links.outgoing).toHaveLength(1);
		expect(links.outgoing[0].target_type).toBe('test_case');
		expect(links.outgoing[0].target_title).toBe(testCase.title);

		// Reverse surface: the test case lists the item as incoming.
		const response = await request.get(`/api/v2/test-cases/${testCase.id}/links`, {
			headers: SEC_FETCH,
		});
		expect(response.ok()).toBeTruthy();
		const payload = (await response.json()) as {
			data: {
				outgoing: Array<Record<string, unknown>>;
				incoming: Array<Record<string, unknown>>;
			};
		};
		const listing = payload.data;
		const incoming = listing.incoming.filter(
			(link) => link.source_type === 'item' && link.source_id === item.id
		);
		expect(incoming).toHaveLength(1);
	});

	test('opens a linked test case preview with the numeric workspace id', async ({
		page,
		request,
	}) => {
		await createLinkViaAPI(request, {
			link_type_id: testsLinkTypeId,
			source_type: 'item',
			source_id: item.id,
			target_type: 'test_case',
			target_id: testCase.id,
		});

		await itemPage.gotoWorkspaceBacklog(workspaceId);
		await itemPage.openItemDetailModal(item.title);
		await linksPage.expectLinkVisible(testCase.title);

		// Capture the detail request whatever the workspace segment is, so a
		// regression that sends the key or null fails the assertion below with a
		// useful message instead of timing out on a request that never matches.
		const detailRequestPromise = page.waitForRequest(
			(candidate) =>
				candidate.method() === 'GET' &&
				/\/api\/v2\/workspaces\/[^/]+\/test-cases\/\d+$/.test(candidate.url())
		);

		const row = linksPage.linkRows().filter({ hasText: testCase.title }).first();
		await row.getByTestId('linked-item-title').click();

		const detailRequest = await detailRequestPromise;
		expect(logicalPath(new URL(detailRequest.url()).pathname)).toBe(
			`/api/v2/workspaces/${workspaceNumericId}/test-cases/${testCase.id}`
		);

		const preview = page.getByTestId('test-case-preview');
		await expect(preview).toContainText(testCase.title);
		await expect(page.getByTestId('test-case-preview-error')).toHaveCount(0);
	});

	test('deletes the test case link via the row hover button', async ({ request }) => {
		await createLinkViaAPI(request, {
			link_type_id: testsLinkTypeId,
			source_type: 'item',
			source_id: item.id,
			target_type: 'test_case',
			target_id: testCase.id,
		});

		await itemPage.gotoWorkspaceBacklog(workspaceId);
		await itemPage.openItemDetailModal(item.title);
		await linksPage.expectLinkVisible(testCase.title);

		await linksPage.deleteLink(testCase.title);
		await linksPage.expectLinkAbsent(testCase.title);

		// Both reverse surfaces must drop the link, not just the UI list.
		const itemLinks = await listLinksForItemViaAPI(request, item.id);
		expect(itemLinks.outgoing).toHaveLength(0);
		await expect
			.poll(async () => {
				const response = await request.get(
					`/api/v2/test-cases/${testCase.id}/links`,
					{ headers: SEC_FETCH }
				);
				expect(response.ok()).toBeTruthy();
				const payload = (await response.json()) as {
					data: {
						incoming: Array<Record<string, unknown>>;
						outgoing: Array<Record<string, unknown>>;
					};
				};
				return payload.data.incoming.length + payload.data.outgoing.length;
			}, { timeout: 20000 })
			.toBe(0);
	});

	test('link-type search scopes to allowed entity types', async () => {
		await itemPage.gotoWorkspaceBacklog(workspaceId);
		await itemPage.openItemDetailModal(item.title);

		await linksPage.openLinkModal();

		// "Tests" allows item + test_case — the test case is found.
		await linksPage.selectLinkType('Tests');
		await linksPage.expectSearchResultCount(testCase.title, 1);

		// "Relates To" searches work items only — the test case must not
		// appear even though its title is unique.
		await linksPage.selectLinkType('Relates To');
		await linksPage.expectSearchResultCount(testCase.title, 0);
	});
});
