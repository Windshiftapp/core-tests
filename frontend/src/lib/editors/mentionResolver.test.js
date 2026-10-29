import { describe, expect, it } from 'vitest';
import { displayNameOf, mentionTextFor, resolveMentionUser } from './mentionResolver.js';

const johnA = { id: 1, username: 'jdoe', first_name: 'John', last_name: 'Doe' };
const johnB = { id: 2, username: 'john.doe', first_name: 'John', last_name: 'Doe' };
const alice = { id: 3, username: 'asmith', first_name: 'Alice', last_name: 'Smith' };
const cher = { id: 4, username: 'cher', first_name: 'Cher', last_name: '' };
const roster = [johnA, johnB, alice, cher];

describe('displayNameOf', () => {
  it('joins first and last name and trims the result', () => {
    expect(displayNameOf(cher)).toBe('Cher');
    expect(displayNameOf({ first_name: '  Jane ', last_name: ' Roe ' })).toBe('Jane   Roe');
    expect(displayNameOf(null)).toBe('');
  });
});

describe('resolveMentionUser', () => {
  it('resolves a username exactly, ignoring case', () => {
    expect(resolveMentionUser(roster, 'JDOE').user).toBe(johnA);
  });

  it('resolves a unique display name', () => {
    expect(resolveMentionUser(roster, 'Alice Smith').user).toBe(alice);
  });

  it('returns every candidate for a duplicated display name instead of guessing', () => {
    const result = resolveMentionUser(roster, 'John Doe');

    expect(result.user).toBeNull();
    expect(result.matches).toEqual([johnA, johnB]);
  });

  it('returns no matches for an unknown identifier', () => {
    expect(resolveMentionUser(roster, 'nobody')).toEqual({ user: null, matches: [] });
  });

  it('returns no matches for an empty identifier', () => {
    expect(resolveMentionUser(roster, '   ')).toEqual({ user: null, matches: [] });
  });
});

describe('mentionTextFor', () => {
  it('keeps a multi-word display name when it is unique', () => {
    expect(mentionTextFor(alice, roster)).toBe('@"Alice Smith"');
  });

  it('uses the username for a single-word display name', () => {
    expect(mentionTextFor(cher, roster)).toBe('@cher');
  });

  it('falls back to the username when another user shares the display name', () => {
    expect(mentionTextFor(johnA, roster)).toBe('@jdoe');
    expect(mentionTextFor(johnB, roster)).toBe('@john.doe');
  });

  it('does not treat the selected user as their own duplicate', () => {
    expect(mentionTextFor(alice, [alice])).toBe('@"Alice Smith"');
  });

  it('detects a duplicate even when roster entries carry no id', () => {
    const a = { username: 'jdoe', first_name: 'John', last_name: 'Doe' };
    const b = { username: 'john.doe', first_name: 'John', last_name: 'Doe' };

    expect(mentionTextFor(a, [a, b])).toBe('@jdoe');
  });

  it('uses the username when the display name is empty', () => {
    expect(mentionTextFor({ id: 9, username: 'ghost' }, roster)).toBe('@ghost');
  });
});
