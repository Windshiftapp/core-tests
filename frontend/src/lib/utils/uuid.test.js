import { afterEach, describe, expect, it, vi } from 'vitest';

import { createId } from './uuid.js';

const UUID_V4_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe('createId', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('uses crypto.randomUUID when available', () => {
    const randomUUID = vi.fn(() => '018e4f2a-0000-7000-8000-000000000000');
    vi.stubGlobal('crypto', { ...globalThis.crypto, randomUUID });

    expect(createId()).toBe('018e4f2a-0000-7000-8000-000000000000');
    expect(randomUUID).toHaveBeenCalledOnce();
  });

  it('falls back to getRandomValues and returns a valid UUID v4', () => {
    vi.stubGlobal('crypto', { getRandomValues: globalThis.crypto.getRandomValues.bind(crypto) });

    const id = createId();

    expect(id).toMatch(UUID_V4_PATTERN);
  });

  it('produces unique ids through the getRandomValues fallback', () => {
    vi.stubGlobal('crypto', { getRandomValues: globalThis.crypto.getRandomValues.bind(crypto) });

    const ids = new Set(Array.from({ length: 1000 }, () => createId()));

    expect(ids.size).toBe(1000);
  });

  it('falls back to Math.random when no crypto API is available', () => {
    vi.stubGlobal('crypto', undefined);

    expect(createId()).toMatch(UUID_V4_PATTERN);
  });
});
