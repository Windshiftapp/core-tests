import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import ts from 'typescript';

const e2eRoot = path.resolve(import.meta.dirname, '..');
const testsRoot = path.join(e2eRoot, 'tests');

function filesUnder(root, predicate) {
  const files = [];
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const fullPath = path.join(root, entry.name);
    if (entry.isDirectory()) files.push(...filesUnder(fullPath, predicate));
    else if (predicate(fullPath)) files.push(fullPath);
  }
  return files;
}

function relativeToE2E(file) {
  return path.relative(e2eRoot, file).split(path.sep).join('/');
}

const browserMethods = new Set([
  'click',
  'dispatchEvent',
  'dragTo',
  'evaluate',
  'fill',
  'focus',
  'getByLabel',
  'getByPlaceholder',
  'getByTestId',
  'getByText',
  'getByRole',
  'goto',
  'hover',
  'insertText',
  'keyboard',
  'locator',
  'press',
  'reload',
  'route',
  'screenshot',
  'selectOption',
  'setViewportSize',
  'type',
  'waitForLoadState',
  'waitForRequest',
  'waitForResponse',
  'waitForSelector',
  'waitForTimeout',
  'waitForURL',
]);

function collectBrowserIdentifiers(source) {
  const identifiers = new Set(['page', 'browser']);

  function markPageAssignment(node, initializer) {
    if (
      ts.isIdentifier(node) &&
      initializer &&
      ts.isNewExpression(initializer) &&
      ts.isIdentifier(initializer.expression) &&
      /Page$/.test(initializer.expression.text)
    ) {
      identifiers.add(node.text);
    }
  }

  function visit(node) {
    if (ts.isVariableDeclaration(node)) {
      markPageAssignment(node.name, node.initializer);
    }
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
      markPageAssignment(node.left, node.right);
    }
    ts.forEachChild(node, visit);
  }

  visit(source);
  return identifiers;
}

function hasBrowserInteractionNode(root, browserIdentifiers = new Set(['page', 'browser'])) {
  let browserOwned = false;

  function hasPageProperty(node) {
    let current = node;
    while (ts.isPropertyAccessExpression(current)) {
      if (current.name.text === 'page') return true;
      current = current.expression;
    }
    return false;
  }

  function isBrowserExpression(node) {
    return (ts.isIdentifier(node) && browserIdentifiers.has(node.text)) || hasPageProperty(node);
  }

  function visit(node) {
    if (browserOwned) return;
    if (ts.isPropertyAccessExpression(node) && node.name.text === 'newPage') {
      browserOwned = true;
      return;
    }
    if (ts.isCallExpression(node)) {
      if (node.arguments.some((argument) => isBrowserExpression(argument))) {
        browserOwned = true;
        return;
      }
      if (ts.isPropertyAccessExpression(node.expression)) {
        let receiver = node.expression.expression;
        while (ts.isPropertyAccessExpression(receiver)) {
          receiver = receiver.expression;
        }
        if (
          ts.isIdentifier(receiver) &&
          (browserIdentifiers.has(receiver.text) || /Page$/.test(receiver.text)) &&
          (browserMethods.has(node.expression.name.text) ||
            browserIdentifiers.has(receiver.text) ||
            /Page$/.test(receiver.text))
        ) {
          browserOwned = true;
          return;
        }
      }
    }
    if (
      ts.isNewExpression(node) &&
      ts.isIdentifier(node.expression) &&
      /Page$/.test(node.expression.text)
    ) {
      browserOwned = true;
      return;
    }
    ts.forEachChild(node, visit);
  }

  visit(root);
  return browserOwned;
}

function parseSource(file) {
  return ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true);
}

function hasBrowserInteraction(file) {
  const source = parseSource(file);
  return hasBrowserInteractionNode(source, collectBrowserIdentifiers(source));
}

function testCallName(expression) {
  if (ts.isIdentifier(expression) && expression.text === 'test') return 'test';
  if (
    ts.isPropertyAccessExpression(expression) &&
    ts.isIdentifier(expression.expression) &&
    expression.expression.text === 'test' &&
    ['only', 'skip', 'fixme'].includes(expression.name.text)
  ) {
    return `test.${expression.name.text}`;
  }
  return null;
}

function declaredTests(file) {
  const source = parseSource(file);
  const browserIdentifiers = collectBrowserIdentifiers(source);
  const entries = [];
  function visit(node) {
    if (ts.isCallExpression(node)) {
      const callName = testCallName(node.expression);
      const firstArgument = node.arguments[0];
      const isConditionalSkip =
        (callName === 'test.skip' || callName === 'test.fixme') &&
        firstArgument &&
        !ts.isStringLiteral(firstArgument) &&
        !ts.isNoSubstitutionTemplateLiteral(firstArgument);
      if (callName && !isConditionalSkip) {
        const callback = [...node.arguments]
          .reverse()
          .find((argument) => ts.isArrowFunction(argument) || ts.isFunctionExpression(argument));
        const name =
          ts.isStringLiteral(firstArgument) || ts.isNoSubstitutionTemplateLiteral(firstArgument)
            ? firstArgument.text
            : '<unnamed>';
        entries.push({
          file,
          name,
          callName,
          browser: callback ? hasBrowserInteractionNode(callback, browserIdentifiers) : false,
        });
      }
    }
    ts.forEachChild(node, visit);
  }

  visit(source);
  return entries;
}

function countDeclaredTests(files) {
  return files.reduce((count, file) => count + declaredTests(file).length, 0);
}

const specs = filesUnder(testsRoot, (file) => file.endsWith('.spec.ts')).sort();
const browserSpecs = specs.filter(hasBrowserInteraction);
const requestOnlySpecs = specs.filter((file) => !hasBrowserInteraction(file));
const tests = specs.flatMap(declaredTests);
const requestOnlyTests = tests.filter((entry) => !entry.browser);
const errors = [];

for (const file of requestOnlySpecs) {
  errors.push(`request-only Playwright spec: ${relativeToE2E(file)}`);
}
console.log(
  `Suite ownership: browser=${browserSpecs.length} specs/${countDeclaredTests(browserSpecs)} declared tests; ` +
    `request-only=${requestOnlySpecs.length} specs/${countDeclaredTests(requestOnlySpecs)} declared tests; ` +
    `request-only callbacks=${requestOnlyTests.length}`
);

for (const entry of requestOnlyTests) {
  console.log(`request-only callback: ${relativeToE2E(entry.file)} :: ${entry.name}`);
}

if (errors.length > 0) {
  for (const error of errors) console.error(`- ${error}`);
  process.exitCode = 1;
}
