import ts from 'typescript';

const FORBIDDEN_QUERY_METHODS = new Set([
  'getByAltText',
  'getByDisplayValue',
  'getByLabel',
  'getByPlaceholder',
  'getByRole',
  'getByText',
  'getByTitle',
]);

const RAW_SELECTOR_METHODS = new Set([
  '$',
  '$$',
  'check',
  'click',
  'dblclick',
  'dispatchEvent',
  'evalOnSelector',
  'evalOnSelectorAll',
  'fill',
  'focus',
  'hover',
  'innerHTML',
  'innerText',
  'inputValue',
  'isChecked',
  'isDisabled',
  'isEditable',
  'isEnabled',
  'isHidden',
  'isVisible',
  'locator',
  'press',
  'selectOption',
  'setAttribute',
  'setChecked',
  'setInputFiles',
  'tap',
  'textContent',
  'type',
  'uncheck',
  'waitForSelector',
]);

function isPageReceiver(node) {
  if (ts.isIdentifier(node)) {
    return node.text === 'page' || node.text === 'pageContext';
  }
  return ts.isPropertyAccessExpression(node) && node.name.text === 'page';
}

function propertyName(node) {
  if (!ts.isPropertyAccessExpression(node)) return '';
  return node.name.text;
}

function selectorText(node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    return node.text;
  }
  if (!ts.isTemplateExpression(node)) return null;
  return [
    node.head.text,
    ...node.templateSpans.flatMap((span) => ['__value__', span.literal.text]),
  ].join('');
}

function stableSelector(selector) {
  const parts = selector.split(',').map((part) => part.trim());
  return (
    parts.length > 0 &&
    parts.every(
      (part) =>
        /^#[A-Za-z_][A-Za-z0-9_.:${}-]*$/.test(part) ||
        /^\[data-testid=(?:"[^"]+"|'[^']+'|[^\]]+)\]$/.test(part)
    )
  );
}

function callRule(node) {
  const method = propertyName(node.expression);
  if (FORBIDDEN_QUERY_METHODS.has(method)) return method;

  if (method === 'filter') {
    const options = node.arguments[0];
    if (
      options &&
      ts.isObjectLiteralExpression(options) &&
      options.properties.some(
        (property) =>
          ts.isPropertyAssignment(property) && propertyNameFromAssignment(property) === 'hasText'
      )
    ) {
      return 'filter.hasText';
    }
  }

  if (!RAW_SELECTOR_METHODS.has(method) || node.arguments.length === 0) {
    return '';
  }
  const receiver = ts.isPropertyAccessExpression(node.expression)
    ? node.expression.expression
    : null;
  if (
    method !== 'locator' &&
    method !== 'waitForSelector' &&
    (!receiver || !isPageReceiver(receiver))
  ) {
    return '';
  }

  // Locator actions such as locator.click() do not take a selector. Only
  // inspect calls whose first argument is a selector-shaped string/template.
  const selector = selectorText(node.arguments[0]);
  if (selector === null) return method === 'locator' ? 'locator.dynamic' : '';
  return stableSelector(selector) ? '' : `${method}.unstable`;
}

function propertyNameFromAssignment(property) {
  if (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name)) {
    return property.name.text;
  }
  return '';
}

export function selectorViolations(sourceText, fileName = 'source.ts') {
  const source = ts.createSourceFile(
    fileName,
    sourceText,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS
  );
  const violations = [];

  function visit(node) {
    if (ts.isCallExpression(node)) {
      const rule = callRule(node);
      if (rule) {
        const location = source.getLineAndCharacterOfPosition(node.getStart(source));
        violations.push({
          rule,
          line: location.line + 1,
          column: location.character + 1,
          source: node.getText(source).replace(/\s+/g, ' ').slice(0, 180),
        });
      }
    }
    ts.forEachChild(node, visit);
  }

  visit(source);
  return violations;
}

export function violationCounts(violations) {
  return Object.fromEntries(
    [...new Set(violations.map((violation) => violation.rule))]
      .sort()
      .map((rule) => [rule, violations.filter((violation) => violation.rule === rule).length])
  );
}
