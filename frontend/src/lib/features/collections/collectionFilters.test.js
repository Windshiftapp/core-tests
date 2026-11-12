import { describe, expect, it } from 'vitest';
import { collectionSubFilterQL } from './collectionFilters.js';

describe('collectionSubFilterQL', () => {
	it('appends the completed-item exclusion by default', () => {
		expect(collectionSubFilterQL()).toBe('status_completed = false');
		expect(collectionSubFilterQL('priority = "High"')).toBe(
			'(priority = "High") AND status_completed = false'
		);
	});

	it('returns the sub-filter unchanged when completed items are included', () => {
		expect(collectionSubFilterQL('', { hideCompleted: false })).toBe('');
		expect(collectionSubFilterQL('priority = "High"', { hideCompleted: false })).toBe(
			'priority = "High"'
		);
	});
});
