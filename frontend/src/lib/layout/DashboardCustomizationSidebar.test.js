import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const aiState = { available: true, enabled: true };

vi.mock('../stores/aiStore.svelte.js', () => ({
  aiStore: {
    get available() {
      return aiState.available;
    },
    isFeatureEnabled: () => aiState.enabled,
    load: vi.fn(),
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import DashboardCustomizationSidebar from './DashboardCustomizationSidebar.svelte';

beforeEach(() => {
  aiState.available = true;
  aiState.enabled = true;
});

// Custom widgets are LLM-generated, so the catalog must not offer them when the
// feature is unavailable.
describe('DashboardCustomizationSidebar custom widget gating', () => {
  it('offers the custom category when generation is available', () => {
    render(DashboardCustomizationSidebar, { isOpen: true, activeCategory: 'activity' });
    expect(screen.getByTestId('customization-category-custom')).toBeInTheDocument();
  });

  it('hides the custom category when no model is configured', () => {
    aiState.available = false;
    render(DashboardCustomizationSidebar, { isOpen: true, activeCategory: 'activity' });
    expect(screen.queryByTestId('customization-category-custom')).not.toBeInTheDocument();
  });

  it('hides the custom category when the feature is disabled', () => {
    aiState.enabled = false;
    render(DashboardCustomizationSidebar, { isOpen: true, activeCategory: 'activity' });
    expect(screen.queryByTestId('customization-category-custom')).not.toBeInTheDocument();
  });
});
