import { describe, expect, it, vi } from 'vitest';
import { useDashboardCustomization } from './useDashboardCustomization.svelte.js';

// WI-1932: the section-editing state machine used by the personal Homepage and
// the workspace Welcome dashboard. Persistence is injected because the two
// pages store their dashboards differently.

function makeCustomization({ sections = [], getSectionDisplay, getSectionSaveValues } = {}) {
  const calls = { updated: [], removed: [] };
  const customization = useDashboardCustomization({
    updateSection: (sectionId, values) => calls.updated.push([sectionId, values]),
    removeSection: (sectionId) => calls.removed.push(sectionId),
    findSection: (sectionId) => sections.find((section) => section.id === sectionId),
    getSectionDisplay:
      getSectionDisplay ?? ((section) => ({ title: section.title, subtitle: section.subtitle })),
    getSectionSaveValues: getSectionSaveValues ?? ((_section, draft) => draft),
  });
  return { customization, calls, sections };
}

describe('useDashboardCustomization', () => {
  it('seeds the draft when a section is created and persists it on save', () => {
    const { customization, calls } = makeCustomization();

    customization.beginNewSection({ id: 'new-1', title: 'Fresh', subtitle: '' });

    expect(customization.editingSectionId).toBe('new-1');
    expect(customization.editingSectionTitle).toBe('Fresh');
    expect(customization.editingSectionSubtitle).toBe('');
    expect(customization.isNewSection).toBe(true);

    customization.editingSectionTitle = 'Renamed';
    customization.editingSectionSubtitle = 'A subtitle';
    customization.save();

    expect(calls.updated).toEqual([['new-1', { title: 'Renamed', subtitle: 'A subtitle' }]]);
    expect(calls.removed).toEqual([]);
    expect(customization.editingSectionId).toBeNull();
    expect(customization.isNewSection).toBe(false);
  });

  it('seeds the draft from the display mapper when editing an existing section', () => {
    const section = { id: 's-1', title: 'builtin.home', subtitle: 'builtin.subtitle' };
    const { customization } = makeCustomization({
      sections: [section],
      getSectionDisplay: () => ({ title: 'Home', subtitle: 'Your dashboard' }),
    });

    customization.startEditing(section);

    expect(customization.editingSectionId).toBe('s-1');
    expect(customization.editingSectionTitle).toBe('Home');
    expect(customization.editingSectionSubtitle).toBe('Your dashboard');
    expect(customization.isNewSection).toBe(false);
  });

  it('routes saved values through the injected save mapper', () => {
    const section = { id: 's-1', title: 'builtin.home', subtitle: '' };
    const { customization, calls } = makeCustomization({
      sections: [section],
      getSectionSaveValues: (target, draft) => ({
        title: draft.title === 'Home' ? target.title : draft.title,
        subtitle: draft.subtitle,
      }),
    });

    customization.startEditing(section);
    customization.save();

    expect(calls.updated).toEqual([['s-1', { title: 'builtin.home', subtitle: '' }]]);
  });

  it('removes a newly created section on cancel', () => {
    const { customization, calls } = makeCustomization();

    customization.beginNewSection({ id: 'new-1', title: 'Fresh', subtitle: '' });
    customization.cancel();

    expect(calls.removed).toEqual(['new-1']);
    expect(calls.updated).toEqual([]);
    expect(customization.editingSectionId).toBeNull();
  });

  it('keeps an existing section on cancel', () => {
    const section = { id: 's-1', title: 'Existing', subtitle: '' };
    const { customization, calls } = makeCustomization({ sections: [section] });

    customization.startEditing(section);
    customization.cancel();

    expect(calls.removed).toEqual([]);
    expect(calls.updated).toEqual([]);
    expect(customization.editingSectionId).toBeNull();
  });

  it('keeps an unsaved new section when the title is non-empty', () => {
    const { customization, calls } = makeCustomization();

    customization.beginNewSection({ id: 'new-1', title: 'Typed', subtitle: '' });
    customization.commitOrDiscardDraft();

    expect(calls.updated).toEqual([['new-1', { title: 'Typed', subtitle: '' }]]);
    expect(calls.removed).toEqual([]);
  });

  it('discards an unsaved new section when the title is blank', () => {
    const { customization, calls } = makeCustomization();

    customization.beginNewSection({ id: 'new-1', title: '   ', subtitle: '' });
    customization.commitOrDiscardDraft();

    expect(calls.removed).toEqual(['new-1']);
    expect(calls.updated).toEqual([]);
    expect(customization.editingSectionId).toBeNull();
  });

  it('only clears the draft for an existing section', () => {
    const section = { id: 's-1', title: 'Existing', subtitle: '' };
    const { customization, calls } = makeCustomization({ sections: [section] });

    customization.startEditing(section);
    customization.commitOrDiscardDraft();

    expect(calls.updated).toEqual([]);
    expect(calls.removed).toEqual([]);
    expect(customization.editingSectionId).toBeNull();
  });

  it('saves on Enter and cancels on Escape', () => {
    const { customization, calls } = makeCustomization();

    customization.beginNewSection({ id: 'new-1', title: 'Fresh', subtitle: '' });

    const escape = { key: 'Escape', preventDefault: vi.fn() };
    customization.handleSectionEditKeydown(escape);
    expect(escape.preventDefault).toHaveBeenCalled();
    expect(calls.removed).toEqual(['new-1']);
    expect(calls.updated).toEqual([]);

    customization.beginNewSection({ id: 'new-2', title: 'Second', subtitle: '' });

    const enter = { key: 'Enter', preventDefault: vi.fn() };
    customization.handleSectionEditKeydown(enter);
    expect(enter.preventDefault).toHaveBeenCalled();
    expect(calls.updated).toEqual([['new-2', { title: 'Second', subtitle: '' }]]);
  });

  it('ignores other keys', () => {
    const { customization, calls } = makeCustomization();

    customization.beginNewSection({ id: 'new-1', title: 'Fresh', subtitle: '' });

    const other = { key: 'a', preventDefault: vi.fn() };
    customization.handleSectionEditKeydown(other);

    expect(other.preventDefault).not.toHaveBeenCalled();
    expect(calls.updated).toEqual([]);
    expect(calls.removed).toEqual([]);
    expect(customization.editingSectionId).toBe('new-1');
  });

  it('does nothing on save without an active draft', () => {
    const { customization, calls } = makeCustomization();

    customization.save();

    expect(calls.updated).toEqual([]);
    expect(calls.removed).toEqual([]);
  });

  it('falls back to the raw draft when the section is gone', () => {
    const { customization, calls } = makeCustomization();

    customization.beginNewSection({ id: 'new-1', title: 'Fresh', subtitle: '' });
    customization.save();

    expect(calls.updated).toEqual([['new-1', { title: 'Fresh', subtitle: '' }]]);
  });
});
