import { describe, expect, it, vi } from 'vitest';
import { loadTestRunDetail } from './testRunDetailData.js';

describe('test run detail request graph', () => {
  it('loads and normalizes the complete run graph with one API request', async () => {
    const apiClient = {
      tests: {
        testRuns: {
          getDetail: vi.fn().mockResolvedValue({
            run: { id: 9, set_id: 4 },
            test_cases: [{ id: 1, test_steps: [{ id: 11 }] }, { id: 2 }],
            results: [{ id: 21, test_case_id: 1 }],
            step_results: { '1_11': { step_id: 11, status: 'passed' } },
          }),
          get: vi.fn(),
          getResults: vi.fn(),
          getStepResults: vi.fn(),
        },
        testSets: {
          get: vi.fn(),
          getTestCases: vi.fn(),
        },
        testCases: { steps: { getAll: vi.fn() } },
      },
    };

    const detail = await loadTestRunDetail(apiClient, 3, 9);

    expect(apiClient.tests.testRuns.getDetail).toHaveBeenCalledOnce();
    expect(apiClient.tests.testRuns.getDetail).toHaveBeenCalledWith(3, 9);
    expect(apiClient.tests.testRuns.get).not.toHaveBeenCalled();
    expect(apiClient.tests.testRuns.getResults).not.toHaveBeenCalled();
    expect(apiClient.tests.testRuns.getStepResults).not.toHaveBeenCalled();
    expect(apiClient.tests.testSets.get).not.toHaveBeenCalled();
    expect(apiClient.tests.testSets.getTestCases).not.toHaveBeenCalled();
    expect(apiClient.tests.testCases.steps.getAll).not.toHaveBeenCalled();
    expect(detail).toEqual({
      run: { id: 9, set_id: 4 },
      testCases: [
        { id: 1, test_steps: [{ id: 11 }] },
        { id: 2, test_steps: [] },
      ],
      results: [{ id: 21, test_case_id: 1 }],
      stepResults: { '1_11': { step_id: 11, status: 'passed' } },
    });
  });

  it('rejects an incomplete aggregate response', async () => {
    const apiClient = {
      tests: { testRuns: { getDetail: vi.fn().mockResolvedValue({}) } },
    };

    await expect(loadTestRunDetail(apiClient, 3, 9)).rejects.toThrow('Test run not found');
  });
});
