import { describe, expect, it } from 'vitest';
import { EDITOR_MARKDOWN_POINT, getEditorProviders } from './extensions.svelte.js';

describe('getEditorProviders', () => {
	const data = {
		[EDITOR_MARKDOWN_POINT]: [
			{ id: 'b', pluginName: 'p', entityTypes: ['item'], order: 20 },
			{ id: 'a', pluginName: 'p', entityTypes: ['item', 'page'], order: 10 },
			{ id: 'any', pluginName: 'p', order: 5 },
			{ id: 'page-only', pluginName: 'p', entityTypes: ['page'], order: 1 },
		],
	};

	it('filters by entity type and sorts by order', () => {
		expect(getEditorProviders(data, 'item').map((provider) => provider.id)).toEqual([
			'any',
			'a',
			'b',
		]);
	});

	it('treats a provider without entityTypes as applying to every surface', () => {
		expect(getEditorProviders(data, 'comment').map((provider) => provider.id)).toEqual(['any']);
	});

	it('returns every provider when no entity type is given', () => {
		expect(getEditorProviders(data, null).map((provider) => provider.id)).toEqual([
			'page-only',
			'any',
			'a',
			'b',
		]);
	});

	it('returns an empty list when the point is absent', () => {
		expect(getEditorProviders({}, 'item')).toEqual([]);
		expect(getEditorProviders(null, 'item')).toEqual([]);
	});

	it('does not mutate the source list', () => {
		const before = data[EDITOR_MARKDOWN_POINT].map((provider) => provider.id);
		getEditorProviders(data, 'item');
		expect(data[EDITOR_MARKDOWN_POINT].map((provider) => provider.id)).toEqual(before);
	});
});
