import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

import { selectorViolations } from '../helpers/selector-policy.mjs';

const e2eRoot = path.resolve(import.meta.dirname, '..');
const manifestPath = path.join(e2eRoot, 'stable-selector-exceptions.json');
const printBaseline = process.argv.includes('--print-baseline');

function filesUnder(root, predicate) {
  if (!fs.existsSync(root)) return [];
  const files = [];
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const fullPath = path.join(root, entry.name);
    if (entry.isDirectory()) files.push(...filesUnder(fullPath, predicate));
    else if (predicate(fullPath)) files.push(fullPath);
  }
  return files;
}

function relative(file) {
  return path.relative(e2eRoot, file).split(path.sep).join('/');
}

function fingerprint(violations) {
  const selectors = violations.map(({ rule, source }) => ({ rule, source }));
  return crypto.createHash('sha256').update(JSON.stringify(selectors)).digest('hex');
}

const files = [
  path.join(e2eRoot, 'global.setup.ts'),
  ...filesUnder(path.join(e2eRoot, 'fixtures'), (file) => file.endsWith('.ts')),
  ...filesUnder(path.join(e2eRoot, 'pages'), (file) => file.endsWith('.ts')),
  ...filesUnder(path.join(e2eRoot, 'tests'), (file) => file.endsWith('.spec.ts')),
].sort();

const findings = new Map();
for (const file of files) {
  const violations = selectorViolations(fs.readFileSync(file, 'utf8'), file);
  if (violations.length > 0) findings.set(relative(file), violations);
}

if (printBaseline) {
  const exceptions = Object.fromEntries(
    [...findings.entries()].map(([file, violations]) => [
      file,
      {
        reason: 'Legacy selector debt; replace with production test IDs when this surface changes.',
        violations: violations.length,
        fingerprint: fingerprint(violations),
      },
    ])
  );
  console.log(JSON.stringify({ schema_version: 1, exceptions }, null, 2));
  process.exit(0);
}

const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
const exceptions = manifest.exceptions ?? {};
const errors = [];

for (const [file, violations] of findings) {
  const exception = exceptions[file];
  if (!exception) {
    for (const violation of violations) {
      errors.push(
        `${file}:${violation.line}:${violation.column} ${violation.rule} — ${violation.source}`
      );
    }
    continue;
  }
  if (!exception.reason || typeof exception.reason !== 'string') {
    errors.push(`${file}: selector exception requires a review reason`);
  }
  const actualFingerprint = fingerprint(violations);
  if (exception.violations !== violations.length || exception.fingerprint !== actualFingerprint) {
    errors.push(
      `${file}: reviewed selector exception changed; expected ${exception.violations ?? '?'} violation(s) with fingerprint ${exception.fingerprint ?? '?'}, got ${violations.length} with fingerprint ${actualFingerprint}`
    );
  }
}

for (const file of Object.keys(exceptions)) {
  if (!findings.has(file)) {
    errors.push(`${file}: stale selector exception (no violations remain)`);
  }
}

const violationTotal = [...findings.values()].reduce(
  (total, violations) => total + violations.length,
  0
);
console.log(
  `Stable-selector policy: ${files.length} source file(s), ${findings.size} reviewed exception file(s), ${violationTotal} legacy violation(s)`
);

if (errors.length > 0) {
  for (const error of errors) console.error(`- ${error}`);
  console.error(
    'Replace the selector with getByTestId()/an exact #id, or review and update stable-selector-exceptions.json.'
  );
  process.exitCode = 1;
}
