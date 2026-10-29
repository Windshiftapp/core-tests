import { describe, expect, it } from 'vitest';
import { conditionFieldScope, stripConditionScope, scopeConditionFieldName, backendFieldName } from '../../../../src/lib/features/actions/shared/fieldNameMapping.js';

// The condition scope prefix is what lets an automation evaluate a field
// against the trigger item's parent (GH #267).
describe('condition field scope helpers', () => {
	it('defaults to the trigger item scope', () => {
		expect(conditionFieldScope('open_child_count')).toBe('item');
		expect(conditionFieldScope(undefined)).toBe('item');
	});

	it('detects the parent scope', () => {
		expect(conditionFieldScope('parent.open_child_count')).toBe('parent');
	});

	it('strips the parent prefix for field picker hydration', () => {
		expect(stripConditionScope('parent.open_child_count')).toBe('open_child_count');
		expect(stripConditionScope('open_child_count')).toBe('open_child_count');
		expect(stripConditionScope(undefined)).toBe('');
	});

	it('applies the parent scope while preserving the field key', () => {
		expect(scopeConditionFieldName('parent.status_id', 'parent')).toBe('parent.status_id');
		expect(scopeConditionFieldName('parent.status_id', 'item')).toBe('status_id');
		expect(scopeConditionFieldName('status_id', 'parent')).toBe('parent.status_id');
		expect(scopeConditionFieldName('', 'parent')).toBe('parent.');
	});

	it('round-trips scope switching on the same field', () => {
		const field = backendFieldName({ id: 'open_child_count' });
		expect(scopeConditionFieldName(field, 'parent')).toBe('parent.open_child_count');
		expect(
			scopeConditionFieldName(scopeConditionFieldName(field, 'parent'), 'item')
		).toBe('open_child_count');
	});
});
