import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { FOREGROUND_BURST, MAX_ACTIVE_BACKGROUND_READS, MAX_ACTIVE_READS, MAX_PENDING_READS, READ_PRIORITY, getReadSchedulerState, resetReadScheduler, scheduleRead } from './readScheduler.js';

// A task that stays pending until its resolver is called. Each job records
// its dispatch order so scheduling policy can be asserted deterministically.
function deferredTask(log, label) {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return {
    label,
    resolve,
    reject,
    task: (signal) => {
      log.push(label);
      return promise;
    },
  };
}

function abortError() {
  return new DOMException('Aborted', 'AbortError');
}

// Dispatch hands each task to a microtask, so log assertions must flush the
// queued dispatches before reading the recorded order.
const flush = () => Promise.resolve().then(() => Promise.resolve());

beforeEach(() => {
  resetReadScheduler();
});

afterEach(() => {
  resetReadScheduler();
  vi.restoreAllMocks();
});

describe('readScheduler — concurrency budget', () => {
  test('caps foreground-active physical attempts at the configured budget', async () => {
    const jobs = Array.from({ length: MAX_ACTIVE_READS + 3 }, (_, i) =>
      deferredTask([], `job-${i}`)
    );
    const promises = jobs.map((job) =>
      scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
    );

    const state = getReadSchedulerState();
    expect(state.active).toBe(MAX_ACTIVE_READS);
    expect(state.pending).toBe(3);

    jobs[0].resolve('done-0');
    await expect(promises[0]).resolves.toBe('done-0');
    await Promise.resolve();
    expect(getReadSchedulerState().active).toBe(MAX_ACTIVE_READS);

    for (const job of jobs) job.resolve(job.label);
    await Promise.all(promises);
    expect(getReadSchedulerState()).toMatchObject({ active: 0, pending: 0 });
  });

  test('caps background-active attempts while foreground work stays unblocked', async () => {
    const backgroundJobs = Array.from({ length: 4 }, (_, i) =>
      deferredTask([], `bg-${i}`)
    );
    const backgroundPromises = backgroundJobs.map((job) =>
      scheduleRead(READ_PRIORITY.BACKGROUND, undefined, job.task)
    );

    expect(getReadSchedulerState()).toMatchObject({
      activeBackground: MAX_ACTIVE_BACKGROUND_READS,
      pendingBackground: 2,
    });

    const foregroundJob = deferredTask([], 'fg-0');
    const foregroundPromise = scheduleRead(READ_PRIORITY.FOREGROUND, undefined, foregroundJob.task);
    expect(getReadSchedulerState().active).toBe(MAX_ACTIVE_BACKGROUND_READS + 1);

    for (const job of [...backgroundJobs, foregroundJob]) job.resolve(job.label);
    await Promise.all([...backgroundPromises, foregroundPromise]);
  });

  test('dispatches three foreground jobs per background job while both are ready', async () => {
    // Fill the budget so the foreground and background jobs below sit in the
    // pending queues together; release blockers to observe the dispatch policy.
    const blockers = Array.from({ length: MAX_ACTIVE_READS }, (_, i) =>
      deferredTask([], `blocker-${i}`)
    );
    const blockerPromises = blockers.map((job) =>
      scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
    );

    const log = [];
    const foregroundJobs = [
      deferredTask(log, 'F1'),
      deferredTask(log, 'F2'),
      deferredTask(log, 'F3'),
    ];
    const backgroundJob = deferredTask(log, 'B1');
    const promises = [
      ...foregroundJobs.map((job) =>
        scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
      ),
      scheduleRead(READ_PRIORITY.BACKGROUND, undefined, backgroundJob.task),
    ];
    expect(getReadSchedulerState().pending).toBe(4);

    for (const job of blockers) {
      job.resolve('blocker');
      await flush();
    }

    expect(FOREGROUND_BURST).toBe(3);
    expect(log).toEqual(['B1', 'F1', 'F2', 'F3']);

    for (const job of [...foregroundJobs, backgroundJob]) job.resolve(job.label);
    await Promise.all([...blockerPromises, ...promises]);
  });

  test('does not starve background work while a foreground backlog never empties', async () => {
    const blockers = Array.from({ length: MAX_ACTIVE_READS }, (_, i) =>
      deferredTask([], `blocker-${i}`)
    );
    const blockerPromises = blockers.map((job) =>
      scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
    );

    const log = [];
    const backgroundJobs = [deferredTask(log, 'B1'), deferredTask(log, 'B2')];
    const foregroundJobs = Array.from({ length: 20 }, (_, i) => deferredTask(log, `F${i}`));
    const promises = [
      ...foregroundJobs.map((job) =>
        scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
      ),
      ...backgroundJobs.map((job) =>
        scheduleRead(READ_PRIORITY.BACKGROUND, undefined, job.task)
      ),
    ];

    // Releasing the eight blockers dispatches eight queued jobs. Background
    // work must be among them instead of waiting for the 20 foreground jobs.
    for (const job of blockers) {
      job.resolve('blocker');
      await flush();
    }

    expect(log.filter((label) => label.startsWith('B')).length).toBe(2);

    for (const job of [...backgroundJobs, ...foregroundJobs]) job.resolve(job.label);
    await Promise.all([...blockerPromises, ...promises]);
  });
});

describe('readScheduler — admission and cleanup', () => {
  test('rejects work beyond the pending capacity with REQUEST_QUEUE_FULL', async () => {
    const activeJobs = Array.from({ length: MAX_ACTIVE_READS }, (_, i) =>
      deferredTask([], `active-${i}`)
    );
    const activePromises = activeJobs.map((job) =>
      scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
    );

    const queued = [];
    for (let i = 0; i < MAX_PENDING_READS; i += 1) {
      const job = deferredTask([], `queued-${i}`);
      queued.push(scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task));
    }

    await expect(
      scheduleRead(READ_PRIORITY.FOREGROUND, undefined, () => Promise.resolve('never'))
    ).rejects.toMatchObject({ status: 0, code: 'REQUEST_QUEUE_FULL' });

    for (const job of activeJobs) job.resolve(job.label);
    resetReadScheduler();
    await Promise.allSettled([...activePromises, ...queued]);
  });

  test('aborting a queued job removes it without dispatching', async () => {
    const activeJobs = Array.from({ length: MAX_ACTIVE_READS }, (_, i) =>
      deferredTask([], `active-${i}`)
    );
    const activePromises = activeJobs.map((job) =>
      scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
    );

    const log = [];
    const controller = new AbortController();
    const queuedJob = deferredTask(log, 'queued');
    const queuedPromise = scheduleRead(READ_PRIORITY.FOREGROUND, controller.signal, queuedJob.task);

    controller.abort();
    await expect(queuedPromise).rejects.toMatchObject({ name: 'AbortError' });
    expect(log).toEqual([]);

    for (const job of activeJobs) job.resolve(job.label);
    await Promise.all(activePromises);
  });

  test('aborting an active job aborts its owned attempt and releases the slot', async () => {
    const controller = new AbortController();
    const observed = [];
    const promise = scheduleRead(READ_PRIORITY.FOREGROUND, controller.signal, (signal) => {
      observed.push(signal.aborted);
      return new Promise((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(abortError()), { once: true });
      });
    });
    // Let the task start so it can observe the owned signal abort.
    await flush();

    expect(getReadSchedulerState().active).toBe(1);
    controller.abort();
    await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
    expect(observed).toEqual([false]);
    expect(getReadSchedulerState().active).toBe(0);
  });

  test('session reset rejects queued work and aborts active work', async () => {
    const activeJob = deferredTask([], 'active');
    const activePromise = scheduleRead(READ_PRIORITY.FOREGROUND, undefined, (signal) => {
      return new Promise((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(abortError()), { once: true });
      });
    });
    const queuedJob = deferredTask([], 'queued');
    const queuedPromise = scheduleRead(READ_PRIORITY.FOREGROUND, undefined, queuedJob.task);

    resetReadScheduler();

    await expect(queuedPromise).rejects.toMatchObject({ name: 'AbortError' });
    await expect(activePromise).rejects.toMatchObject({ name: 'AbortError' });
    expect(getReadSchedulerState()).toMatchObject({ active: 0, pending: 0 });
    // Silence the unused deferred resolvers.
    activeJob.resolve('active');
    queuedJob.resolve('queued');
  });

  test('holds the slot through response-body consumption, not just fetch', async () => {
    let releaseBody;
    const bodyReady = new Promise((resolve) => {
      releaseBody = resolve;
    });
    const blockers = Array.from({ length: MAX_ACTIVE_READS - 1 }, (_, i) =>
      deferredTask([], `blocker-${i}`)
    );
    const blockerPromises = blockers.map((job) =>
      scheduleRead(READ_PRIORITY.FOREGROUND, undefined, job.task)
    );
    const bodyPromise = scheduleRead(READ_PRIORITY.FOREGROUND, undefined, async () => {
      await bodyReady;
      return 'body';
    });
    const last = deferredTask([], 'last');
    const lastPromise = scheduleRead(READ_PRIORITY.FOREGROUND, undefined, last.task);

    expect(getReadSchedulerState()).toMatchObject({ active: MAX_ACTIVE_READS, pending: 1 });

    // The slot is not released when the fetch resolves; it is released when the
    // task (fetch plus body read) settles.
    releaseBody();
    await expect(bodyPromise).resolves.toBe('body');
    expect(getReadSchedulerState()).toMatchObject({ active: MAX_ACTIVE_READS, pending: 0 });

    for (const job of blockers) job.resolve(job.label);
    last.resolve('done');
    await Promise.all([...blockerPromises, lastPromise]);
  });
});
