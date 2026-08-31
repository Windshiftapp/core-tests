import { describe, expect, it } from 'vitest';
import { mergePageUpdate } from './pageState.js';

describe('mergePageUpdate', () => {
  it('preserves hydrated labels when an update response omits them', () => {
    const labels = [{ id: 7, name: 'Founding', color: '#2563eb' }];

    expect(
      mergePageUpdate(
        { id: 3, title: 'Draft', content: 'Body', labels },
        { id: 3, title: 'Draft', content: 'Body', metadata: { icon: 'Check' } }
      )
    ).toEqual({
      id: 3,
      title: 'Draft',
      content: 'Body',
      metadata: { icon: 'Check' },
      labels,
    });
  });

  it('uses labels returned by the update endpoint when present', () => {
    const updatedLabels = [{ id: 8, name: 'Legal', color: '#059669' }];

    expect(
      mergePageUpdate(
        { id: 3, labels: [{ id: 7, name: 'Founding' }] },
        { id: 3, labels: updatedLabels }
      ).labels
    ).toBe(updatedLabels);
  });

  it('can preserve a newer local content draft', () => {
    expect(
      mergePageUpdate(
        { id: 3, labels: [] },
        { id: 3, content: 'Saved snapshot' },
        'Newer local draft'
      ).content
    ).toBe('Newer local draft');
  });
});
