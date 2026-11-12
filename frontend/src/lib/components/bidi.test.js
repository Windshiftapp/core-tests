import { render } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import Badge from './Badge.svelte';
import Chip from './Chip.svelte';
import ItemKey from './ItemKey.svelte';
import Lozenge from './Lozenge.svelte';
import NativeSelect from './NativeSelect.svelte';
import Text from './Text.svelte';
import TruncatedFieldValue from './TruncatedFieldValue.svelte';

afterEach(() => {
  document.body.innerHTML = '';
});

describe('bidi direction on shared text primitives', () => {
  it('Text follows its content direction unless explicitly aligned', () => {
    const { container } = render(Text, { props: { as: 'p', children: () => 'Hello' } });
    expect(container.querySelector('p')).toHaveAttribute('dir', 'auto');

    const aligned = render(Text, {
      props: { as: 'p', align: 'center', children: () => 'Centered' },
    });
    expect(aligned.container.querySelector('p')).not.toHaveAttribute('dir');
  });

  it('Badge, Chip, and Lozenge follow their content direction', () => {
    const badge = render(Badge, { props: { children: () => 'Active' } });
    expect(badge.container.firstElementChild).toHaveAttribute('dir', 'auto');

    const chip = render(Chip, { props: { children: () => 'Frontend' } });
    expect(chip.container.firstElementChild).toHaveAttribute('dir', 'auto');

    const lozenge = render(Lozenge, { props: { text: 'Tag' } });
    expect(lozenge.container.firstElementChild).toHaveAttribute('dir', 'auto');
  });

  it('ItemKey stays left-to-right inside an RTL interface', () => {
    const { container } = render(ItemKey, {
      props: { itemKey: 'WI-123', clickable: false },
    });
    expect(container.firstElementChild).toHaveAttribute('dir', 'ltr');
  });

  it('NativeSelect follows the selected option direction', () => {
    const { container } = render(NativeSelect, {
      props: { value: 'a', options: [{ value: 'a', label: 'Option' }] },
    });
    expect(container.querySelector('select')).toHaveAttribute('dir', 'auto');
  });

  it('TruncatedFieldValue keeps end alignment while following content', () => {
    const { container } = render(TruncatedFieldValue, {
      props: { value: 'عنوان', testId: 'tv' },
    });
    const value = container.querySelector('[data-testid="tv"]');
    expect(value).toHaveAttribute('dir', 'auto');
    expect(value.getAttribute('style')).toContain('text-align: end');
  });
});
