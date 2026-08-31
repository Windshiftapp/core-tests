import { describe, expect, it } from 'vitest';

import {
  captureWorkspaceWidgetWidths,
  normalizeWorkspaceWidgets,
  restoreRejectedWorkspaceWidgetWidths,
} from './workspaceWidgetLayout.js';

describe('workspace widget layout widths', () => {
  it('normalizes loaded widths to the workspace API contract', () => {
    const widgets = normalizeWorkspaceWidgets([
      { id: 'too-wide', type: 'recent-items', width: 12 },
      { id: 'too-narrow', type: 'recent-items', width: 0 },
      { id: 'invalid', type: 'created-chart', width: 'invalid' },
    ]);

    expect(widgets.map((widget) => widget.width)).toEqual([3, 1, 1]);
  });

  it('rolls back a rejected width without overwriting a newer edit', () => {
    const savedWidths = captureWorkspaceWidgetWidths([
      { id: 'rejected', width: 1 },
      { id: 'changed-again', width: 1 },
    ]);
    const rejectedWidgets = [
      { id: 'rejected', width: 3 },
      { id: 'changed-again', width: 3 },
    ];
    const currentWidgets = [
      { id: 'rejected', type: 'recent-items', width: 3 },
      { id: 'changed-again', type: 'recent-items', width: 2 },
    ];

    expect(
      restoreRejectedWorkspaceWidgetWidths(currentWidgets, rejectedWidgets, savedWidths)
    ).toEqual([
      { id: 'rejected', type: 'recent-items', width: 1 },
      { id: 'changed-again', type: 'recent-items', width: 2 },
    ]);
  });
});
