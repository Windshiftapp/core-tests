import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/tests/index.js', () => ({
  tests: {
        testCases: {
          validateFeature: vi.fn(),
        },
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, vars) =>
    vars && 'scenario' in vars
      ? `${key}:${vars.scenario}`
      : vars && 'line' in vars
        ? `${key}:${vars.line}:${vars.column}`
        : key,
}));

import { tests } from '../../api/tests/index.js';
import GherkinField from './GherkinField.svelte';

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => cleanup());

function renderField(value = '') {
  return render(GherkinField, { props: { value, workspaceId: 3 } });
}

describe('GherkinField', () => {
  it('validates through the backend and reports a valid scenario', async () => {
    tests.testCases.validateFeature.mockResolvedValue({
      valid: true,
      errors: [],
      document: { feature_name: 'Login', scenarios: [{ name: 'valid credentials' }] },
    });
    const { component } = renderField();
    // Bindable prop starts empty; drive it through the rendered textarea.
    const textarea = screen.getByTestId('test-case-gherkin-source');
    fireEvent.input(textarea, { target: { value: 'Feature: Login\n  Scenario: valid credentials' } });

    fireEvent.click(screen.getByTestId('test-case-gherkin-validate'));

    await waitFor(() =>
      expect(tests.testCases.validateFeature).toHaveBeenCalledWith(
        3,
        'Feature: Login\n  Scenario: valid credentials'
      )
    );
    await waitFor(() =>
      expect(screen.getByTestId('test-case-gherkin-valid').textContent).toContain(
        'testing.gherkinValidScenario:valid credentials'
      )
    );
  });

  it('lists parse errors with their authored positions', async () => {
    tests.testCases.validateFeature.mockResolvedValue({
      valid: false,
      errors: [
        { line: 3, column: 5, message: 'step keyword expected' },
        { line: 4, column: 1, message: 'unexpected end of file' },
      ],
      document: null,
    });
    renderField('Feature: Login');
    fireEvent.click(screen.getByTestId('test-case-gherkin-validate'));

    const errors = await screen.findAllByTestId('test-case-gherkin-errors');
    await waitFor(() => expect(errors[0].children.length).toBe(2));
    expect(errors[0].textContent).toContain('testing.gherkinErrorLocation:3:5');
    expect(errors[0].textContent).toContain('step keyword expected');
  });

  it('flags edits made after a successful validation', async () => {
    tests.testCases.validateFeature.mockResolvedValue({
      valid: true,
      errors: [],
      document: { feature_name: 'Login', scenarios: [{ name: 's' }] },
    });
    renderField('Feature: Login\n  Scenario: s');
    fireEvent.click(screen.getByTestId('test-case-gherkin-validate'));
    await waitFor(() => expect(screen.getByTestId('test-case-gherkin-valid')).toBeTruthy());

    fireEvent.input(screen.getByTestId('test-case-gherkin-source'), {
      target: { value: 'Feature: Login\n  Scenario: s2' },
    });

    await waitFor(() => expect(screen.getByTestId('test-case-gherkin-stale')).toBeTruthy());
  });
});
