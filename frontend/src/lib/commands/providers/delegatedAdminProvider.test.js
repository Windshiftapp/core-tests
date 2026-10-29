import { describe, expect, test } from 'vitest';
import { BUCKET } from '../buckets.js';
import { delegatedAdminProvider } from './delegatedAdminProvider.js';

const t = (key) => key;

function commandsFor(permissions) {
  return delegatedAdminProvider({ t, permissions });
}

describe('delegatedAdminProvider', () => {
  test('emits the channels management surface for a channel manager', () => {
    const commands = commandsFor({ isSystemAdmin: false, canManageChannels: true });

    expect(commands).toHaveLength(1);
    expect(commands[0]).toMatchObject({
      id: 'manage-channels',
      label: 'nav.channels',
      bucket: BUCKET.ADMIN,
      url: '/manage/channels',
    });
  });

  test('emits both management surfaces in registry order', () => {
    const commands = commandsFor({
      isSystemAdmin: false,
      canManageChannels: true,
      canManageAssets: true,
    });

    expect(commands.map((command) => command.url)).toEqual(['/manage/channels', '/manage/assets']);
  });

  test('emits nothing for a system admin, who uses the admin panel', () => {
    expect(
      commandsFor({ isSystemAdmin: true, canManageChannels: true, canManageAssets: true })
    ).toEqual([]);
  });

  test('emits nothing without a management capability', () => {
    expect(commandsFor({ isSystemAdmin: false })).toEqual([]);
  });

  test('tolerates an unhydrated permission state', () => {
    expect(commandsFor(undefined)).toEqual([]);
  });
});
