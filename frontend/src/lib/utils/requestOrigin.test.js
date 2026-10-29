import { describe, expect, it } from 'vitest';
import { isExternalRequest } from './requestOrigin.js';

describe('isExternalRequest', () => {
  it('treats portal and form submissions (request type) as external', () => {
    expect(isExternalRequest({ request_type_id: 5, creator_portal_customer_id: null })).toBe(true);
  });

  it('treats helpdesk email intake (creator customer, no request type) as external', () => {
    expect(isExternalRequest({ request_type_id: null, creator_portal_customer_id: 9 })).toBe(true);
  });

  it('treats agent-created internal items as internal', () => {
    expect(isExternalRequest({ request_type_id: null, creator_portal_customer_id: null })).toBe(false);
  });

  it('handles missing items', () => {
    expect(isExternalRequest(null)).toBe(false);
    expect(isExternalRequest(undefined)).toBe(false);
  });
});
