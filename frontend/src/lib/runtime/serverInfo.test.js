import { afterEach, describe, expect, it } from 'vitest';

import { isServerBaseURLConfigured, serverBaseURL } from './serverInfo.js';

describe('serverBaseURL', () => {
  afterEach(() => {
    delete window.__WINDSHIFT_BASE_URL__;
    delete window.__WINDSHIFT_BASE_URL_CONFIGURED__;
  });

  it('returns null when the server did not inject a base URL', () => {
    expect(serverBaseURL()).toBeNull();
    expect(isServerBaseURLConfigured()).toBeNull();
  });

  it('returns the injected base URL without a trailing slash', () => {
    window.__WINDSHIFT_BASE_URL__ = 'https://windshift.example.test/';

    expect(serverBaseURL()).toBe('https://windshift.example.test');
  });

  it('reports whether the base URL was explicitly configured', () => {
    window.__WINDSHIFT_BASE_URL__ = 'http://localhost:8080';
    window.__WINDSHIFT_BASE_URL_CONFIGURED__ = false;

    expect(serverBaseURL()).toBe('http://localhost:8080');
    expect(isServerBaseURLConfigured()).toBe(false);

    window.__WINDSHIFT_BASE_URL_CONFIGURED__ = true;
    expect(isServerBaseURLConfigured()).toBe(true);
  });
});
