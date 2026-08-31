import { describe, expect, it } from 'vitest';
import {
  clearPublicFormDraft,
  loadPublicFormDraft,
  loadPublicFormDraftForIdentity,
  publicFormDraftKey,
  savePublicFormDraft,
} from './publicFormDrafts.js';

function memoryStorage() {
  const values = new Map();
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
}

function failingWriteStorage() {
  const values = new Map();
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: () => {
      throw new Error('QuotaExceededError');
    },
    removeItem: (key) => values.delete(key),
  };
}

const DAY = 24 * 60 * 60 * 1000;

describe('public form browser drafts', () => {
  it('round-trips all editable values and the current step', () => {
    const storage = memoryStorage();
    savePublicFormDraft(storage, 'support requests', 7, {
      title: 'Printer issue',
      description: 'Third floor',
      custom_fields: { priority: 'high', confirmed: true },
      current_step: 2,
    });

    expect(loadPublicFormDraft(storage, 'support requests', 7)).toMatchObject({
      version: 1,
      title: 'Printer issue',
      description: 'Third floor',
      custom_fields: { priority: 'high', confirmed: true },
      current_step: 2,
    });
    expect(publicFormDraftKey('support requests', 7)).toContain('support%20requests:7');
  });

  it('does not persist empty forms and can start fresh', () => {
    const storage = memoryStorage();
    expect(
      savePublicFormDraft(storage, 'support', 7, {
        title: '',
        description: '',
        custom_fields: { confirmed: false },
        current_step: 1,
      })
    ).toBeNull();

    savePublicFormDraft(storage, 'support', 7, {
      title: 'Saved',
      custom_fields: {},
      current_step: 1,
    });
    savePublicFormDraft(storage, 'support', 7, {
      title: '',
      description: '',
      custom_fields: { confirmed: false },
      current_step: 1,
    });
    expect(loadPublicFormDraft(storage, 'support', 7)).toBeNull();

    savePublicFormDraft(storage, 'support', 7, {
      title: 'Saved again',
      custom_fields: {},
      current_step: 1,
    });
    clearPublicFormDraft(storage, 'support', 7);
    expect(loadPublicFormDraft(storage, 'support', 7)).toBeNull();
  });

  it('fails closed for malformed or obsolete stored data', () => {
    const storage = memoryStorage();
    storage.setItem(publicFormDraftKey('support', 7), '{broken');
    expect(loadPublicFormDraft(storage, 'support', 7)).toBeNull();
    storage.setItem(publicFormDraftKey('support', 7), JSON.stringify({ version: 99 }));
    expect(loadPublicFormDraft(storage, 'support', 7)).toBeNull();
  });

  it('expires and purges drafts older than the retention window', () => {
    const storage = memoryStorage();
    const key = publicFormDraftKey('support', 7);
    const stale = {
      version: 1,
      title: 'Stale entry',
      description: '',
      custom_fields: {},
      current_step: 1,
      updated_at: new Date(Date.now() - 8 * DAY).toISOString(),
    };
    storage.setItem(key, JSON.stringify(stale));

    expect(loadPublicFormDraft(storage, 'support', 7)).toBeNull();
    // The expired draft is purged from storage on load, not merely ignored.
    expect(storage.getItem(key)).toBeNull();
  });

  it('treats drafts without a timestamp as expired', () => {
    const storage = memoryStorage();
    storage.setItem(
      publicFormDraftKey('support', 7),
      JSON.stringify({
        version: 1,
        title: 'No timestamp',
        custom_fields: {},
        current_step: 1,
      })
    );
    expect(loadPublicFormDraft(storage, 'support', 7)).toBeNull();
  });

  it('still loads a draft saved just inside the retention window', () => {
    const storage = memoryStorage();
    savePublicFormDraft(storage, 'support', 7, {
      title: 'Fresh enough',
      custom_fields: {},
      current_step: 1,
    });
    // Backdate to just under the boundary so it remains valid.
    const stored = JSON.parse(storage.getItem(publicFormDraftKey('support', 7)));
    stored.updated_at = new Date(Date.now() - 6 * DAY).toISOString();
    storage.setItem(publicFormDraftKey('support', 7), JSON.stringify(stored));

    expect(loadPublicFormDraft(storage, 'support', 7)).toMatchObject({
      title: 'Fresh enough',
    });
  });

  it('isolates authenticated drafts by user identity', () => {
    const storage = memoryStorage();
    savePublicFormDraft(
      storage,
      'support',
      7,
      {
        title: 'Alice draft',
        custom_fields: {},
        current_step: 1,
      },
      'alice'
    );

    // Alice can load her own draft.
    expect(loadPublicFormDraft(storage, 'support', 7, 'alice')).toMatchObject({
      title: 'Alice draft',
    });
    // Bob cannot see Alice's draft.
    expect(loadPublicFormDraft(storage, 'support', 7, 'bob')).toBeNull();
    // An anonymous visitor cannot see Alice's draft.
    expect(loadPublicFormDraft(storage, 'support', 7)).toBeNull();
    // Each identity gets a distinct key.
    expect(publicFormDraftKey('support', 7, 'alice')).not.toBe(
      publicFormDraftKey('support', 7, 'bob')
    );
  });

  it('hands an anonymous session draft to the user after sign-in', () => {
    const anonymousStorage = memoryStorage();
    const authenticatedStorage = memoryStorage();
    savePublicFormDraft(anonymousStorage, 'support', 7, {
      title: 'Started before sign-in',
      custom_fields: { urgency: 'high' },
      current_step: 2,
    });

    expect(
      loadPublicFormDraftForIdentity({
        anonymousStorage,
        authenticatedStorage,
        slug: 'support',
        formId: 7,
        userId: 'alice',
      })
    ).toMatchObject({
      title: 'Started before sign-in',
      custom_fields: { urgency: 'high' },
      current_step: 2,
    });
    expect(loadPublicFormDraft(anonymousStorage, 'support', 7)).toBeNull();
    expect(loadPublicFormDraft(authenticatedStorage, 'support', 7, 'alice')).toMatchObject({
      title: 'Started before sign-in',
    });
  });

  it('keeps an existing user draft instead of overwriting it during sign-in', () => {
    const anonymousStorage = memoryStorage();
    const authenticatedStorage = memoryStorage();
    savePublicFormDraft(anonymousStorage, 'support', 7, {
      title: 'Anonymous draft',
      custom_fields: {},
      current_step: 1,
    });
    savePublicFormDraft(
      authenticatedStorage,
      'support',
      7,
      { title: 'Alice draft', custom_fields: {}, current_step: 2 },
      'alice'
    );

    expect(
      loadPublicFormDraftForIdentity({
        anonymousStorage,
        authenticatedStorage,
        slug: 'support',
        formId: 7,
        userId: 'alice',
      })
    ).toMatchObject({ title: 'Alice draft', current_step: 2 });
    expect(loadPublicFormDraft(anonymousStorage, 'support', 7)).toMatchObject({
      title: 'Anonymous draft',
    });
  });

  it('clears only the requesting identity draft', () => {
    const storage = memoryStorage();
    savePublicFormDraft(
      storage,
      'support',
      7,
      {
        title: 'Alice',
        custom_fields: {},
        current_step: 1,
      },
      'alice'
    );
    savePublicFormDraft(
      storage,
      'support',
      7,
      {
        title: 'Bob',
        custom_fields: {},
        current_step: 1,
      },
      'bob'
    );

    clearPublicFormDraft(storage, 'support', 7, 'alice');
    expect(loadPublicFormDraft(storage, 'support', 7, 'alice')).toBeNull();
    expect(loadPublicFormDraft(storage, 'support', 7, 'bob')).toMatchObject({
      title: 'Bob',
    });
  });

  it('survives storage write failures without throwing', () => {
    const storage = failingWriteStorage();
    expect(() =>
      savePublicFormDraft(storage, 'support', 7, {
        title: 'Will not persist',
        custom_fields: {},
        current_step: 1,
      })
    ).not.toThrow();
    expect(
      savePublicFormDraft(storage, 'support', 7, {
        title: 'Will not persist',
        custom_fields: {},
        current_step: 1,
      })
    ).toBeNull();
  });

  it('does not throw when storage is unavailable', () => {
    expect(() => loadPublicFormDraft(null, 'support', 7)).not.toThrow();
    expect(() =>
      savePublicFormDraft(null, 'support', 7, {
        title: 'x',
        custom_fields: {},
        current_step: 1,
      })
    ).not.toThrow();
    expect(() => clearPublicFormDraft(null, 'support', 7)).not.toThrow();
  });
});
