import { describe, expect, it } from 'vitest';
import {
  getZammadObservedValueLabel,
  getZammadStatusAppearance,
  getZammadStatusBucketDisplayLabel,
  getZammadStatusBucketLabel,
  isCurrentZammadWorkspaceOverviewRequest,
} from './zammadObservations.js';

const translate = (key, params = {}) => `${key}:${params.id ?? ''}`;

describe('Zammad observation formatting', () => {
  it('distinguishes missing names and IDs from the initial value', () => {
    expect(getZammadObservedValueLabel({ id: 17, name: ' ' }, translate)).toBe('zammad.timeline.valueIdFallback:17');
    expect(getZammadObservedValueLabel({ id: 0, name: '' }, translate)).toBe('zammad.timeline.initialValue:');
    expect(getZammadStatusBucketLabel({ id: 9, name: '' }, translate)).toBe('zammad.overview.statusIdFallback:9');
    expect(getZammadStatusBucketLabel({ id: null, name: '' }, translate)).toBe('zammad.overview.unknownStatusBucket:');
  });

  it('localizes only the explicit unassigned owner sentinel in historical changes', () => {
    expect(getZammadObservedValueLabel({ id: 1, name: 'Unassigned' }, translate, 'owner')).toBe('zammad.unassignedOwner:');
    expect(getZammadObservedValueLabel({ id: 0, name: '' }, translate, 'owner')).toBe('zammad.timeline.initialValue:');
    expect(getZammadObservedValueLabel({ id: 1, name: 'new' }, translate, 'status')).toBe('new');
    expect(getZammadObservedValueLabel({ id: 1, name: 'Unassigned' }, translate, 'group')).toBe('Unassigned');
  });

  it('shows each status bucket with its connection and appearance', () => {
    const render = (key, params = {}) => key === 'zammad.overview.statusWithConnection'
      ? `${params.connection} · ${params.status}` : translate(key, params);
    expect(getZammadStatusBucketDisplayLabel({ id: 2, name: 'open', connection_name: 'Helpdesk' }, render)).toBe('Helpdesk · open');
    expect(getZammadStatusAppearance({ id: 3, name: 'pending reminder' }, false)).toBe('warning');
    expect(getZammadStatusAppearance({ id: 4, name: 'closed' }, true)).toBe('success');
  });

  it('rejects an overview response from an older workspace', () => {
    expect(isCurrentZammadWorkspaceOverviewRequest(1, 2, 7, 8)).toBe(false);
    expect(isCurrentZammadWorkspaceOverviewRequest(2, 2, 7, 8)).toBe(false);
    expect(isCurrentZammadWorkspaceOverviewRequest(2, 2, 8, 8)).toBe(true);
  });
});
