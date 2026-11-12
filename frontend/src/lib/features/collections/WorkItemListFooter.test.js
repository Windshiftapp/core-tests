import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
  i18n: { locale: 'en-US' },
}));

import WorkItemListFooter from './WorkItemListFooter.svelte';

afterEach(cleanup);

describe('WorkItemListFooter', () => {
  test('renders the pager and reports page changes with the effective page size', async () => {
    const onpageChange = vi.fn();
    const onpageSizeChange = vi.fn();

    render(WorkItemListFooter, {
      props: {
        pagination: { page: 1, total_items: 3, limit: 2 },
        itemCount: 2,
        onpageChange,
        onpageSizeChange,
      },
    });

    await fireEvent.click(screen.getByTestId('pagination-next'));

    expect(onpageChange).toHaveBeenCalledTimes(1);
    expect(onpageChange).toHaveBeenCalledWith({ detail: { page: 2, itemsPerPage: 2 } });
  });

  test('renders nothing when the result set is not pageable', () => {
    render(WorkItemListFooter, {
      props: { pagination: { page: 1, total_items: 0, limit: 50 }, itemCount: 0 },
    });

    expect(screen.queryByTestId('pagination-next')).toBeNull();
  });

  test('renders nothing when no pagination metadata is available', () => {
    render(WorkItemListFooter, { props: { pagination: null, itemCount: 5 } });

    expect(screen.queryByTestId('pagination-next')).toBeNull();
  });
});
