import { describe, expect, it } from 'vitest';

import { resolveBoardWorkspaces } from './boardWorkspaces.js';

const directory = [
  { id: 1, name: 'Alpha' },
  { id: 2, name: 'Beta' },
  { id: 3, name: 'Gamma' },
];

describe('resolveBoardWorkspaces', () => {
  it('returns nothing until the board scope has loaded', () => {
    expect(
      resolveBoardWorkspaces({
        scopeLoaded: false,
        allowsAllWorkspaces: false,
        directoryWorkspaces: directory,
        scopedIds: [1, 2],
        currentWorkspace: { id: 1, name: 'Alpha' },
      })
    ).toEqual([]);
  });

  it('returns the full directory when the board spans all workspaces', () => {
    expect(
      resolveBoardWorkspaces({
        scopeLoaded: true,
        allowsAllWorkspaces: true,
        directoryWorkspaces: directory,
        scopedIds: [1, 2],
        currentWorkspace: { id: 1, name: 'Alpha' },
      })
    ).toEqual(directory);
  });

  it('filters the directory to the board scope', () => {
    expect(
      resolveBoardWorkspaces({
        scopeLoaded: true,
        allowsAllWorkspaces: false,
        directoryWorkspaces: directory,
        scopedIds: [2, 3],
        currentWorkspace: { id: 2, name: 'Beta' },
      })
    ).toEqual([
      { id: 2, name: 'Beta' },
      { id: 3, name: 'Gamma' },
    ]);
  });

  it('keeps the board workspace when it falls outside the cached directory page', () => {
    // The directory cache holds the first server page only (WI-1442); a
    // workspace-scoped board whose workspace sorts past it must still be a
    // quick-add target.
    const currentWorkspace = { id: 99, name: 'Zulu' };
    expect(
      resolveBoardWorkspaces({
        scopeLoaded: true,
        allowsAllWorkspaces: false,
        directoryWorkspaces: directory,
        scopedIds: [99],
        currentWorkspace,
      })
    ).toEqual([currentWorkspace]);
  });

  it('does not duplicate a board workspace that is already cached', () => {
    expect(
      resolveBoardWorkspaces({
        scopeLoaded: true,
        allowsAllWorkspaces: false,
        directoryWorkspaces: directory,
        scopedIds: [2],
        currentWorkspace: { id: 2, name: 'Beta' },
      })
    ).toEqual([{ id: 2, name: 'Beta' }]);
  });

  it('does not add a workspace the board scope excludes', () => {
    expect(
      resolveBoardWorkspaces({
        scopeLoaded: true,
        allowsAllWorkspaces: false,
        directoryWorkspaces: directory,
        scopedIds: [1],
        currentWorkspace: { id: 99, name: 'Zulu' },
      })
    ).toEqual([{ id: 1, name: 'Alpha' }]);
  });
});
