import { render } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';

import Licenses from './Licenses.svelte';

describe('Licenses', () => {
  it('renders runtime and transitive scopes with distinct badge tones', () => {
    const { container } = render(Licenses);
    const scopeBadges = [...container.querySelectorAll('.scope-pill')];
    const runtimeBadge = scopeBadges.find((badge) => badge.textContent?.trim() === 'runtime');
    const transitiveBadge = scopeBadges.find((badge) => badge.textContent?.trim() === 'transitive');

    expect(runtimeBadge).toHaveClass('scope-pill-runtime');
    expect(transitiveBadge).toHaveClass('scope-pill-transitive');
    expect(runtimeBadge?.className).not.toBe(transitiveBadge?.className);
  });
});
