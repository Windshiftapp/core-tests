import { describe, expect, test } from 'vitest';
import { workspaceMenuItems } from './workspaceMenu.js';

const t = (key) => key;

const workspaces = [
  { id: 1, name: 'Alpha', description: 'First', active: true },
  { id: 2, name: 'Bravo', description: 'Second', active: true },
  { id: 3, name: 'Charlie', description: 'Third', active: true },
  { id: 4, name: 'Delta', description: 'Fourth', active: true },
];

function workspaceTitles(items) {
  return items
    .filter((item) => item.type === 'group')
    .flatMap((group) => group.items.map((item) => item.title));
}

describe('workspace menu recent ordering', () => {
  test('recently visited workspaces lead while the rest keep directory order', () => {
    const items = workspaceMenuItems(workspaces, '', () => {}, t, [3, 1]);

    expect(workspaceTitles(items)).toEqual(['Charlie', 'Alpha', 'Bravo', 'Delta']);
  });

  test('keeps the directory order when no recent workspace IDs are supplied', () => {
    const items = workspaceMenuItems(workspaces, '', () => {}, t);

    expect(workspaceTitles(items)).toEqual(['Alpha', 'Bravo', 'Charlie', 'Delta']);
  });

  test('ignores recent IDs that are not in the visible workspace list', () => {
    const items = workspaceMenuItems(workspaces, '', () => {}, t, [99, 2]);

    expect(workspaceTitles(items)).toEqual(['Bravo', 'Alpha', 'Charlie', 'Delta']);
  });

  test('applies recent ordering to the filtered results while searching', () => {
    const items = workspaceMenuItems(workspaces, 'ha', () => {}, t, [3, 1]);

    expect(workspaceTitles(items)).toEqual(['Charlie', 'Alpha']);
  });
});
