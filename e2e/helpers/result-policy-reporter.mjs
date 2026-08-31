import fs from 'node:fs';
import path from 'node:path';

function normalizedPath(file) {
  return String(file || '').replaceAll('\\', '/');
}

function testIdentity(test) {
  return {
    id: test.id,
    title: test.titlePath().join(' › '),
    file: normalizedPath(test.location.file),
    line: test.location.line,
  };
}

export function renderGitHubSummary(summary) {
  const { counts } = summary;
  const lines = [
    '## E2E result policy',
    '',
    '| Total | Passed | Failed | Skipped | Unexpected skips | Passed on retry |',
    '| ---: | ---: | ---: | ---: | ---: | ---: |',
    `| ${counts.total} | ${counts.passed} | ${counts.failed} | ${counts.skipped} | ${counts.unexpected_skips} | ${counts.passed_on_retry} |`,
  ];
  const noteworthy = [
    ...summary.unexpected_skips.map((entry) => ['Unexpected skip', entry]),
    ...summary.passed_on_retry.map((entry) => ['Passed on retry', entry]),
    ...summary.failed_tests.map((entry) => ['Failed', entry]),
  ];
  if (noteworthy.length > 0) {
    lines.push('', '### Noteworthy results', '');
    for (const [kind, entry] of noteworthy) {
      lines.push(`- ${kind}: \`${entry.file}:${entry.line}\` — ${entry.title}`);
    }
  }
  return `${lines.join('\n')}\n`;
}

export default class ResultPolicyReporter {
  constructor(options = {}) {
    this.outputFile = options.outputFile || 'e2e-summary.json';
    this.githubSummaryFile = options.githubSummaryFile || '';
    this.failOnUnexpectedSkip = options.failOnUnexpectedSkip === true;
    this.failOnRetry = options.failOnRetry === true;
    this.results = new Map();
  }

  onTestEnd(test, result) {
    const entry = this.results.get(test.id) || {
      ...testIdentity(test),
      attempts: [],
    };
    entry.attempts.push({
      retry: result.retry,
      status: result.status,
      duration_ms: result.duration,
    });
    this.results.set(test.id, entry);
  }

  async onEnd(fullResult) {
    const entries = [...this.results.values()].map((entry) => ({
      ...entry,
      final_status: entry.attempts.at(-1)?.status || 'unknown',
    }));
    const skipped = entries.filter((entry) => entry.final_status === 'skipped');
    const expectedSkips = [];
    const unexpectedSkips = skipped;
    const passedOnRetry = entries.filter(
      (entry) =>
        entry.final_status === 'passed' &&
        (entry.attempts.length > 1 || entry.attempts.some((attempt) => attempt.retry > 0))
    );
    const failed = entries.filter((entry) =>
      ['failed', 'timedOut', 'interrupted'].includes(entry.final_status)
    );

    const counts = {
      total: entries.length,
      passed: entries.filter((entry) => entry.final_status === 'passed').length,
      failed: failed.length,
      skipped: skipped.length,
      expected_skips: expectedSkips.length,
      unexpected_skips: unexpectedSkips.length,
      passed_on_retry: passedOnRetry.length,
    };
    const policyFailed =
      (this.failOnUnexpectedSkip && unexpectedSkips.length > 0) ||
      (this.failOnRetry && passedOnRetry.length > 0);
    const summary = {
      schema_version: 1,
      generated_at: new Date().toISOString(),
      playwright_status: fullResult.status,
      policy_status: policyFailed ? 'failed' : 'passed',
      policy: {
        fail_on_unexpected_skip: this.failOnUnexpectedSkip,
        fail_on_retry: this.failOnRetry,
      },
      counts,
      unexpected_skips: unexpectedSkips,
      expected_skips: expectedSkips,
      passed_on_retry: passedOnRetry,
      failed_tests: failed,
    };

    fs.mkdirSync(path.dirname(path.resolve(this.outputFile)), {
      recursive: true,
    });
    fs.writeFileSync(this.outputFile, `${JSON.stringify(summary, null, 2)}\n`);
    if (this.githubSummaryFile) {
      fs.appendFileSync(this.githubSummaryFile, renderGitHubSummary(summary));
    }

    console.log(
      `E2E result policy: ${counts.unexpected_skips} unexpected skip(s), ` +
        `${counts.expected_skips} expected skip(s), ${counts.passed_on_retry} pass-on-retry test(s)`
    );
    for (const entry of unexpectedSkips) {
      console.error(`Unexpected skip: ${entry.file}:${entry.line} — ${entry.title}`);
    }
    for (const entry of passedOnRetry) {
      console.warn(`Passed on retry: ${entry.file}:${entry.line} — ${entry.title}`);
    }

    if (policyFailed) return { status: 'failed' };
    return undefined;
  }
}
