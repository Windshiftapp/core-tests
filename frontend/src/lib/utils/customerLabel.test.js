import { describe, expect, test } from 'vitest';

import { NO_CUSTOMER_BUILTIN_KEY, customerDisplayName, customerOrganisationName, isNoCustomer, realCustomers } from './customerLabel.js';

const t = (key) => (key === 'time.noCustomer' ? 'No customer' : key);

describe('isNoCustomer', () => {
  test('matches only the built-in placeholder', () => {
    expect(isNoCustomer({ builtin_key: NO_CUSTOMER_BUILTIN_KEY })).toBe(true);
    expect(isNoCustomer({ builtin_key: '' })).toBe(false);
    expect(isNoCustomer({ name: 'Acme' })).toBe(false);
    expect(isNoCustomer(null)).toBe(false);
  });
});

describe('customerDisplayName', () => {
  test('localizes the placeholder and keeps real customer names', () => {
    expect(customerDisplayName('No customer', NO_CUSTOMER_BUILTIN_KEY, t)).toBe('No customer');
    expect(customerDisplayName('Acme', '', t)).toBe('Acme');
    expect(customerDisplayName('', '', t)).toBe('');
  });

  test('localizes an organisation object by its builtin key', () => {
    expect(customerOrganisationName({ name: 'No customer', builtin_key: NO_CUSTOMER_BUILTIN_KEY }, t)).toBe(
      'No customer'
    );
    expect(customerOrganisationName({ name: 'Acme' }, t)).toBe('Acme');
  });
});

describe('realCustomers', () => {
  test('drops the built-in placeholder and tolerates a missing list', () => {
    const customers = [{ id: 1, name: 'Acme' }, { id: 2, name: 'No customer', builtin_key: NO_CUSTOMER_BUILTIN_KEY }];
    expect(realCustomers(customers)).toEqual([{ id: 1, name: 'Acme' }]);
    expect(realCustomers(null)).toEqual([]);
  });
});
