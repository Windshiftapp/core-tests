import { describe, expect, it } from 'vitest';
import { BUCKET, bucketRank } from './buckets.js';
import { compareCommands, scoreCommand, scoreText } from './score.js';

describe('scoreText', () => {
  it('ranks exact match highest', () => {
    expect(scoreText('board', 'Board')).toBeGreaterThan(scoreText('board', 'Open Board'));
  });

  it('ranks token match above substring containment', () => {
    // "Open Board" has 'board' as a token; "Dashboard" only contains 'board' as substring.
    expect(scoreText('board', 'Open Board')).toBeGreaterThan(scoreText('board', 'Dashboard'));
  });

  it('regression: board ranks above dashboard', () => {
    const openBoard = scoreText('board', 'Open Acme Board');
    const dashboard = scoreText('board', 'Dashboard');
    expect(openBoard).toBeGreaterThan(dashboard);
  });

  it('ranks prefix-of-token above substring-inside-token', () => {
    // 'Backlog' has a token starting with 'back' (prefix tier).
    // 'feedback' contains 'back' only as substring inside a single token.
    expect(scoreText('back', 'Backlog')).toBeGreaterThan(scoreText('back', 'feedback'));
  });

  it('falls back to fuzzy match capped below substring tier', () => {
    const fuzzy = scoreText('abc', 'a_b_c_extra');
    expect(fuzzy).toBeGreaterThan(0);
    expect(fuzzy).toBeLessThan(scoreText('abc', 'abcdef'));
  });

  it('returns 0 when characters cannot all be found in order', () => {
    expect(scoreText('xyz', 'Workspace Board')).toBe(0);
  });

  it('handles empty inputs gracefully', () => {
    expect(scoreText('', 'Anything')).toBe(0);
    expect(scoreText('q', '')).toBe(0);
  });
});

describe('scoreCommand', () => {
  it('uses the best of label / keywords / description', () => {
    const cmd = {
      label: 'Open Board',
      description: 'View workspace items as columns',
      keywords: ['kanban', 'cards'],
    };
    expect(scoreCommand('board', cmd)).toBeGreaterThan(0);
    expect(scoreCommand('kanban', cmd)).toBeGreaterThan(0);
    expect(scoreCommand('columns', cmd)).toBeGreaterThan(0);
  });

  it('decays keyword score by index so earlier keywords matter more', () => {
    const first = scoreCommand('z', { label: 'X', keywords: ['z', 'a', 'b', 'c', 'd'] });
    const last = scoreCommand('z', { label: 'X', keywords: ['a', 'b', 'c', 'd', 'z'] });
    expect(first).toBeGreaterThan(last);
  });

  it('returns 0 for empty query', () => {
    expect(scoreCommand('', { label: 'X', keywords: [] })).toBe(0);
  });
});

describe('bucketRank', () => {
  it('returns default order without a query', () => {
    expect(bucketRank(BUCKET.ITEM_ACTIONS, '')).toBeLessThan(
      bucketRank(BUCKET.GLOBAL_NAVIGATION, '')
    );
    expect(bucketRank(BUCKET.GLOBAL_NAVIGATION, '')).toBeLessThan(
      bucketRank(BUCKET.SEARCH_RESULTS, '')
    );
  });

  it('promotes search-results above global-navigation for item-key queries', () => {
    expect(bucketRank(BUCKET.SEARCH_RESULTS, 'ABC-123')).toBeLessThan(
      bucketRank(BUCKET.GLOBAL_NAVIGATION, 'ABC-123')
    );
  });

  it('keeps default order for non-item-key queries', () => {
    expect(bucketRank(BUCKET.GLOBAL_NAVIGATION, 'board')).toBeLessThan(
      bucketRank(BUCKET.SEARCH_RESULTS, 'board')
    );
  });
});

describe('compareCommands', () => {
  it('orders by bucket first, then score', () => {
    const cmds = [
      { id: 'a', bucket: BUCKET.GLOBAL_NAVIGATION, _score: 200, _seq: 0 },
      { id: 'b', bucket: BUCKET.ITEM_ACTIONS, _score: 50, _seq: 1 },
      { id: 'c', bucket: BUCKET.WORKSPACE_NAVIGATION, _score: 100, _seq: 2 },
    ];
    cmds.sort(compareCommands(''));
    expect(cmds.map((c) => c.id)).toEqual(['b', 'c', 'a']);
  });

  it('breaks score ties by insertion order', () => {
    const cmds = [
      { id: 'late', bucket: BUCKET.GLOBAL_NAVIGATION, _score: 100, _seq: 5 },
      { id: 'early', bucket: BUCKET.GLOBAL_NAVIGATION, _score: 100, _seq: 1 },
    ];
    cmds.sort(compareCommands(''));
    expect(cmds.map((c) => c.id)).toEqual(['early', 'late']);
  });
});
