import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        validateGoalQuery: vi.fn(),
      },
}))

vi.mock('../../api/queryLanguage.js', () => ({
  queryLanguage: {
        getCatalog: vi.fn().mockResolvedValue({}),
        getValues: vi.fn().mockResolvedValue([]),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../pickers/CategoryMultiSelect.svelte', () => ({ default: () => null }));
vi.mock('../shared/QlQueryBar.svelte', () => ({ default: () => null }));

import { sla } from '../../api/sla.js';
import SlaMetricEditor from './SlaMetricEditor.svelte';

import { queryLanguage } from '../../api/queryLanguage.js';


const metric = {
  id: 1,
  name: 'First response',
  display_format: 'time',
  position: 0,
  is_active: true,
  conditions: [],
  goals: [
    {
      ql_query: 'priority = High',
      import_status: 'native',
      targets: [{ is_fallback: true, target_ms: 3600000, calendar_id: 1 }],
    },
  ],
};

const calendars = [{ id: 1, name: 'Support hours' }];

describe('SlaMetricEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('blocks the save when a goal query is rejected by the parser', async () => {
    sla.validateGoalQuery.mockResolvedValue('Unexpected token');
    const onSave = vi.fn();

    render(SlaMetricEditor, { isOpen: true, metric, calendars, workspaceId: 5, onSave });

    await fireEvent.click(screen.getByTestId('sla-metric-submit'));

    await waitFor(() => expect(sla.validateGoalQuery).toHaveBeenCalledWith(5, 'priority = High'));
    expect(onSave).not.toHaveBeenCalled();
    expect(await screen.findByTestId('sla-metric-error')).toBeTruthy();
  });

  it.each([
    { label: 'minutes', ms: 1800000 },
    { label: 'fractional hours', ms: 5400000 },
    { label: 'hours', ms: 10800000 },
    { label: 'days', ms: 86400000 },
  ])('preserves a $label target through an unrelated edit (WI-1577)', async ({ ms }) => {
    sla.validateGoalQuery.mockResolvedValue(null);
    const onSave = vi.fn().mockResolvedValue(undefined);
    const metricWithTarget = {
      ...metric,
      goals: [
        {
          ql_query: 'priority = High',
          import_status: 'native',
          targets: [{ is_fallback: true, target_ms: ms, calendar_id: 1 }],
        },
      ],
    };

    render(SlaMetricEditor, {
      isOpen: true,
      metric: metricWithTarget,
      calendars,
      workspaceId: 5,
      onSave,
    });

    await fireEvent.input(screen.getByTestId('sla-metric-name'), {
      target: { value: 'Renamed metric' },
    });
    await fireEvent.click(screen.getByTestId('sla-metric-submit'));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].goals[0].targets[0].target_ms).toBe(ms);
  });

  it('blocks the save when the metric has no goals', async () => {
    const onSave = vi.fn();

    render(SlaMetricEditor, { isOpen: true, metric: null, calendars, workspaceId: 5, onSave });

    await fireEvent.input(screen.getByTestId('sla-metric-name'), {
      target: { value: 'No goal metric' },
    });
    await fireEvent.click(screen.getByTestId('sla-metric-submit'));

    expect(onSave).not.toHaveBeenCalled();
    expect((await screen.findByTestId('sla-metric-error')).textContent).toContain('goalRequired');
  });

  it('saves a goal with a blank query as a match-all target', async () => {
    sla.validateGoalQuery.mockResolvedValue(null);
    const onSave = vi.fn().mockResolvedValue(undefined);
    const matchAll = {
      ...metric,
      goals: [
        {
          ql_query: '',
          import_status: 'native',
          targets: [{ is_fallback: true, target_ms: 3600000, calendar_id: 1 }],
        },
      ],
    };

    render(SlaMetricEditor, { isOpen: true, metric: matchAll, calendars, workspaceId: 5, onSave });

    await fireEvent.click(screen.getByTestId('sla-metric-submit'));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(sla.validateGoalQuery).not.toHaveBeenCalled();
    expect(onSave.mock.calls[0][0].goals[0].ql_query).toBe('');
  });

  it('saves the goal payload once every query parses', async () => {
    sla.validateGoalQuery.mockResolvedValue(null);
    const onSave = vi.fn().mockResolvedValue(undefined);

    render(SlaMetricEditor, { isOpen: true, metric, calendars, workspaceId: 5, onSave });

    await fireEvent.click(screen.getByTestId('sla-metric-submit'));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    const payload = onSave.mock.calls[0][0];
    expect(payload.name).toBe('First response');
    expect(payload.goals).toHaveLength(1);
    expect(payload.goals[0].ql_query).toBe('priority = High');
    expect(payload.goals[0].targets[0].calendar_id).toBe(1);
    expect(payload.goals[0].targets[0].target_ms).toBe(3600000);
  });
});
