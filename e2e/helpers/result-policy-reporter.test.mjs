import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import ResultPolicyReporter, { renderGitHubSummary } from './result-policy-reporter.mjs';

function testCase(id, file, title) {
  return {
    id,
    location: { file, line: 12 },
    titlePath: () => ['chromium', title],
  };
}

function result(status, retry = 0) {
  return { status, retry, duration: 10 };
}

function reporterOptions(outputFile, overrides = {}) {
  return {
    outputFile,
    failOnUnexpectedSkip: true,
    failOnRetry: false,
    ...overrides,
  };
}

function silenceReporterOutput(t) {
  const original = {
    error: console.error,
    log: console.log,
    warn: console.warn,
  };
  console.error = () => {};
  console.log = () => {};
  console.warn = () => {};
  t.after(() => {
    console.error = original.error;
    console.log = original.log;
    console.warn = original.warn;
  });
}

test('unexpected skips fail policy and are persisted with location', async (t) => {
  silenceReporterOutput(t);
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'e2e-policy-'));
  t.after(() => fs.rmSync(tempDir, { recursive: true, force: true }));
  const outputFile = path.join(tempDir, 'summary.json');
  const reporter = new ResultPolicyReporter(reporterOptions(outputFile));
  reporter.onTestEnd(
    testCase('skip-1', '/repo/e2e/tests/mobile-surface.spec.ts', 'opens an assigned item'),
    result('skipped')
  );

  assert.deepEqual(await reporter.onEnd({ status: 'passed' }), {
    status: 'failed',
  });
  const summary = JSON.parse(fs.readFileSync(outputFile, 'utf8'));
  assert.equal(summary.counts.unexpected_skips, 1);
  assert.equal(summary.unexpected_skips[0].line, 12);
});

test('pass-on-retry tests are identified and can be enforced', async (t) => {
  silenceReporterOutput(t);
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'e2e-policy-'));
  t.after(() => fs.rmSync(tempDir, { recursive: true, force: true }));
  const outputFile = path.join(tempDir, 'summary.json');
  const reporter = new ResultPolicyReporter(
    reporterOptions(outputFile, {
      failOnUnexpectedSkip: false,
      failOnRetry: true,
    })
  );
  const flaky = testCase('retry-1', '/repo/e2e/tests/items.spec.ts', 'creates an item');
  reporter.onTestEnd(flaky, result('failed', 0));
  reporter.onTestEnd(flaky, result('passed', 1));

  assert.deepEqual(await reporter.onEnd({ status: 'passed' }), {
    status: 'failed',
  });
  const summary = JSON.parse(fs.readFileSync(outputFile, 'utf8'));
  assert.equal(summary.counts.passed_on_retry, 1);
  assert.deepEqual(
    summary.passed_on_retry[0].attempts.map((attempt) => attempt.status),
    ['failed', 'passed']
  );
  assert.match(renderGitHubSummary(summary), /Passed on retry/);
  assert.match(renderGitHubSummary(summary), /items\.spec\.ts:12/);
});

test('appends compact counts to the GitHub job summary', async (t) => {
  silenceReporterOutput(t);
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'e2e-policy-'));
  t.after(() => fs.rmSync(tempDir, { recursive: true, force: true }));
  const outputFile = path.join(tempDir, 'summary.json');
  const githubSummaryFile = path.join(tempDir, 'github-summary.md');
  const reporter = new ResultPolicyReporter({
    ...reporterOptions(outputFile),
    githubSummaryFile,
  });
  reporter.onTestEnd(
    testCase('pass-1', '/repo/e2e/tests/items.spec.ts', 'creates an item'),
    result('passed')
  );

  await reporter.onEnd({ status: 'passed' });
  const markdown = fs.readFileSync(githubSummaryFile, 'utf8');
  assert.match(markdown, /\| 1 \| 1 \| 0 \| 0 \| 0 \| 0 \|/);
});
