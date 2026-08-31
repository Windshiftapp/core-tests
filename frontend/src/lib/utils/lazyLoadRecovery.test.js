import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  findDeployedBuild,
  hasSessionExpired,
  LAZY_LOAD_SESSION_CHECK_TIMEOUT_MS,
  reloadIfBuildChanged,
  STALE_BUILD_RELOAD_KEY,
} from './lazyLoadRecovery.js';

describe('hasSessionExpired', () => {
  it('identifies an unauthenticated session response', async () => {
    const checkSession = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error('expired'), { status: 401 }));

    await expect(hasSessionExpired(checkSession)).resolves.toBe(true);
    expect(checkSession).toHaveBeenCalledWith({ timeout: LAZY_LOAD_SESSION_CHECK_TIMEOUT_MS });
  });

  it('does not treat connectivity failures as an expired session', async () => {
    const checkSession = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error('offline'), { status: 0 }));

    await expect(hasSessionExpired(checkSession)).resolves.toBe(false);
  });

  it('keeps a valid session authenticated', async () => {
    await expect(hasSessionExpired(vi.fn().mockResolvedValue({ user: {} }))).resolves.toBe(false);
  });
});

const SHELL_URL = 'https://windshift.test/';

/** A shell document as the server emits it: inline theme script, then the
 * hashed entry chunk that identifies the build. */
function shellHTML(entry) {
  return `<!doctype html><html><head><script>window.__WINDSHIFT_CONTEXT_PATH__="";</script><script type="module" crossorigin src="${entry}"></script></head><body></body></html>`;
}

function shellDoc(entry) {
  return new DOMParser().parseFromString(shellHTML(entry), 'text/html');
}

function servingShell(entry) {
  return vi.fn().mockResolvedValue({ ok: true, text: async () => shellHTML(entry) });
}

function fakeStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    values,
  };
}

describe('findDeployedBuild', () => {
  it('reports the deployed build when the server was redeployed', async () => {
    const fetchImpl = servingShell('./_app/index-DCj0I5Rh.js');

    await expect(
      findDeployedBuild({ fetchImpl, doc: shellDoc('./_app/index-B2i45HSq.js'), url: SHELL_URL })
    ).resolves.toBe('./_app/index-DCj0I5Rh.js');
    expect(fetchImpl).toHaveBeenCalledWith(SHELL_URL, expect.objectContaining({ cache: 'reload' }));
  });

  it('reports nothing while the page runs the deployed build', async () => {
    await expect(
      findDeployedBuild({
        fetchImpl: servingShell('./_app/index-B2i45HSq.js'),
        doc: shellDoc('./_app/index-B2i45HSq.js'),
        url: SHELL_URL,
      })
    ).resolves.toBeNull();
  });

  it('reports nothing when the shell cannot be fetched', async () => {
    const offline = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));

    await expect(
      findDeployedBuild({
        fetchImpl: offline,
        doc: shellDoc('./_app/index-B2i45HSq.js'),
        url: SHELL_URL,
      })
    ).resolves.toBeNull();
  });

  it('reports nothing when the shell request errors', async () => {
    const failing = vi.fn().mockResolvedValue({ ok: false, status: 502, text: async () => '' });

    await expect(
      findDeployedBuild({
        fetchImpl: failing,
        doc: shellDoc('./_app/index-B2i45HSq.js'),
        url: SHELL_URL,
      })
    ).resolves.toBeNull();
  });
});

describe('reloadIfBuildChanged', () => {
  let reload;
  let storage;

  beforeEach(() => {
    reload = vi.fn();
    storage = fakeStorage();
  });

  function run(runningEntry, deployedEntry) {
    return reloadIfBuildChanged({
      storage,
      reload,
      fetchImpl: servingShell(deployedEntry),
      doc: shellDoc(runningEntry),
      url: SHELL_URL,
    });
  }

  it('reloads onto a newly deployed build', async () => {
    await expect(run('./_app/index-B2i45HSq.js', './_app/index-DCj0I5Rh.js')).resolves.toBe(true);

    expect(reload).toHaveBeenCalledTimes(1);
    expect(storage.getItem(STALE_BUILD_RELOAD_KEY)).toBe('./_app/index-DCj0I5Rh.js');
  });

  it('leaves a current build alone', async () => {
    await expect(run('./_app/index-B2i45HSq.js', './_app/index-B2i45HSq.js')).resolves.toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });

  it('does not loop when the reloaded build still fails to load a chunk', async () => {
    storage = fakeStorage({ [STALE_BUILD_RELOAD_KEY]: './_app/index-DCj0I5Rh.js' });

    await expect(run('./_app/index-B2i45HSq.js', './_app/index-DCj0I5Rh.js')).resolves.toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });

  it('reloads again once a later build is deployed', async () => {
    storage = fakeStorage({ [STALE_BUILD_RELOAD_KEY]: './_app/index-DCj0I5Rh.js' });

    await expect(run('./_app/index-DCj0I5Rh.js', './_app/index-Cx0f9Qa1.js')).resolves.toBe(true);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('still reloads when storage is unavailable', async () => {
    storage = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => {
        throw new Error('denied');
      },
    };

    await expect(run('./_app/index-B2i45HSq.js', './_app/index-DCj0I5Rh.js')).resolves.toBe(true);
    expect(reload).toHaveBeenCalledTimes(1);
  });
});
