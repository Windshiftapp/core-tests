import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, test, vi } from 'vitest';

// i18n.t — return the key verbatim so assertions don't depend on locale data.
vi.mock('../../stores/i18n.svelte.js', () => ({
  t: vi.fn((key) => key),
}));

import DataTable from '../../components/DataTable.svelte';

// Mirror exactly how TestReports renders the Recent Failures table: a column
// whose cell is a `slot`, and the slot snippet reads the row POSITIONALLY.
// DataTable invokes slots as `slot(item, column)`, so a raw snippet receives a
// getter for the row as its first parameter — same contract as the markup
// snippet `{#snippet test_case_link(item)} ... {item.test_case_id} ...`.
function testCaseLinkSnippet() {
  return createRawSnippet((itemGetter) => ({
    render: () => {
      const item = itemGetter();
      return `<a href="/cases/${item.test_case_id}">${item.test_case_title}</a>`;
    },
  }));
}

const columns = [{ key: 'test_case_title', label: 'Test Case', slot: 'test_case_link' }];

// The prod trigger: the same flaky test case (id 5) failed across two runs, so
// `test_case_id` repeats. Rows carry a unique composite `row_key` (run:case),
// which is what TestReports keys the table on.
const rows = [
  {
    test_case_id: 5,
    test_case_title: 'Login test',
    run_id: 10,
    run_name: 'Run A',
    row_key: '10:5',
  },
  {
    test_case_id: 5,
    test_case_title: 'Login test',
    run_id: 11,
    run_name: 'Run B',
    row_key: '11:5',
  },
];

describe('TestReports failures table (regression)', () => {
  test('renders every row when test_case_id is duplicated across runs', () => {
    // Before the fix this threw "Cannot read properties of undefined
    // (reading 'test_case_id')": duplicate keys crashed the keyed {#each}, and
    // the slot was declared `({ item })` so it never received the row.
    render(DataTable, {
      props: {
        columns,
        data: rows,
        keyField: 'row_key',
        test_case_link: testCaseLinkSnippet(),
      },
    });

    // Both duplicate-test-case rows render, each linking to its own run.
    const links = screen.getAllByRole('link', { name: 'Login test' });
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAttribute('href', '/cases/5');
  });
});
