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
  it('embeds the user id and display name', () => {
    expect(mentionTextFor(alice)).toBe('@{Alice Smith#3}');
  });

  it('embeds the id for a single-word display name', () => {
    expect(mentionTextFor(cher)).toBe('@{Cher#4}');
  });

  it('embeds the id even when another user shares the display name', () => {
    expect(mentionTextFor(johnA)).toBe('@{John Doe#1}');
    expect(mentionTextFor(johnB)).toBe('@{John Doe#2}');
  });

  it('falls back to the username when the display name is empty', () => {
    expect(mentionTextFor({ id: 9, username: 'ghost' })).toBe('@{ghost#9}');
  });

  it('falls back to the legacy name syntax when there is no id', () => {
    expect(mentionTextFor({ username: 'jdoe', first_name: 'John', last_name: 'Doe' })).toBe(
      '@"John Doe"'
    );
    expect(mentionTextFor({ username: 'cher' })).toBe('@cher');
  });
});

