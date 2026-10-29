import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/tests/index.js', () => ({
  tests: {
    testRuns: { getDetail: vi.fn(), get: vi.fn(), getResults: vi.fn(), getStepResults: vi.fn() },
    testPlans: { get: vi.fn(), getTestCases: vi.fn() },
    testCases: { steps: { getAll: vi.fn() } },
  },
}));

import { tests } from '../../api/tests/index.js';
import { loadTestRunDetail } from './testRunDetailData.js';

describe('test run detail request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads and normalizes the complete run graph with one API request', async () => {
    tests.testRuns.getDetail.mockResolvedValue({
      run: { id: 9, set_id: 4 },
      test_cases: [{ id: 1, test_steps: [{ id: 11 }] }, { id: 2 }],
      results: [{ id: 21, test_case_id: 1 }],
      step_results: [{ test_case_id: 1, step_id: 11, status: 'passed' }],
      bdd_snapshots: [{ test_case_id: 2, gherkin: 'Feature: …', spec: '{}' }],
      example_results: [
        { run_id: 9, test_case_id: 2, example_index: 0, status: 'passed' },
        { run_id: 9, test_case_id: 2, example_index: 1, status: 'not_run' },
      ],
    });

    const detail = await loadTestRunDetail(3, 9);

    expect(tests.testRuns.getDetail).toHaveBeenCalledOnce();
    expect(tests.testRuns.getDetail).toHaveBeenCalledWith(3, 9);
    expect(tests.testRuns.get).not.toHaveBeenCalled();
    expect(tests.testRuns.getResults).not.toHaveBeenCalled();
    expect(tests.testRuns.getStepResults).not.toHaveBeenCalled();
    expect(tests.testPlans.get).not.toHaveBeenCalled();
    expect(tests.testPlans.getTestCases).not.toHaveBeenCalled();
    expect(tests.testCases.steps.getAll).not.toHaveBeenCalled();
    expect(detail).toEqual({
      run: { id: 9, set_id: 4 },
      testCases: [
        { id: 1, test_steps: [{ id: 11 }] },
        { id: 2, test_steps: [] },
      ],
      results: [{ id: 21, test_case_id: 1 }],
      stepResults: { '1_11': { test_case_id: 1, step_id: 11, status: 'passed' } },
      bddSnapshots: { 2: { test_case_id: 2, gherkin: 'Feature: …', spec: '{}' } },
      bddExampleResults: [
        { run_id: 9, test_case_id: 2, example_index: 0, status: 'passed' },
        { run_id: 9, test_case_id: 2, example_index: 1, status: 'not_run' },
      ],
    });
  });

  it('rejects an incomplete aggregate response', async () => {
    tests.testRuns.getDetail.mockResolvedValue({});

    await expect(loadTestRunDetail(3, 9)).rejects.toThrow('Test run not found');
  });
});
