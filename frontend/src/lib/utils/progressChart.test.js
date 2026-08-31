import { describe, expect, it } from 'vitest';
import { calculatePercentComplete } from './progressChart.js';

describe('calculatePercentComplete', () => {
  it('shows 100% when every item is complete even if the supplied percentage is stale', () => {
    expect(calculatePercentComplete(7, 7, 0)).toBe(100);
  });

  it('calculates partial completion from item counts', () => {
    expect(calculatePercentComplete(2, 3, 0)).toBe(67);
  });

  it('uses the supplied percentage when item counts are unavailable', () => {
    expect(calculatePercentComplete(undefined, undefined, 42)).toBe(42);
  });
});
