import { describe, expect, it, vi } from 'vitest';
import { createPageAutosaveQueue } from './pageAutosaveQueue.js';

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

const sameContent = (left, right) => left.title === right.title && left.content === right.content;

describe('createPageAutosaveQueue', () => {
  it('persists the newest edit made while the same page is saving', async () => {
    const firstSave = deferred();
    const saved = [];
    const save = vi.fn(async (snapshot) => {
      saved.push(snapshot);
      if (snapshot.content === 'first') await firstSave.promise;
    });
    const queue = createPageAutosaveQueue(save, sameContent);

    const draining = queue.enqueue(7, { title: 'Page', content: 'first' });
    queue.enqueue(7, { title: 'Page', content: 'second' });
    queue.enqueue(7, { title: 'Page', content: 'latest' });

    expect(saved).toEqual([{ title: 'Page', content: 'first' }]);
    firstSave.resolve();
    await draining;

    expect(saved).toEqual([
      { title: 'Page', content: 'first' },
      { title: 'Page', content: 'latest' },
    ]);
  });

  it('retains saves for different pages during rapid navigation', async () => {
    const firstSave = deferred();
    const saved = [];
    const queue = createPageAutosaveQueue(async (snapshot) => {
      saved.push(snapshot);
      if (snapshot.pageId === 1) await firstSave.promise;
    });

    const draining = queue.enqueue(1, { pageId: 1, content: 'one' });
    queue.enqueue(2, { pageId: 2, content: 'two' });
    queue.enqueue(3, { pageId: 3, content: 'three' });
    firstSave.resolve();
    await draining;

    expect(saved.map((snapshot) => snapshot.pageId)).toEqual([1, 2, 3]);
  });

  it('does not enqueue an identical snapshot behind its in-flight save', async () => {
    const firstSave = deferred();
    const save = vi.fn(async () => firstSave.promise);
    const queue = createPageAutosaveQueue(save, sameContent);
    const snapshot = { title: 'Page', content: 'unchanged' };

    const draining = queue.enqueue(7, snapshot);
    queue.enqueue(7, { ...snapshot });
    firstSave.resolve();
    await draining;

    expect(save).toHaveBeenCalledTimes(1);
  });
});
