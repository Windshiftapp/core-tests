import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  requestTypes: [],
  assetReports: [],
}));

vi.mock('@atlaskit/pragmatic-drag-and-drop/element/adapter', () => ({
  draggable: vi.fn(() => () => {}),
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../stores/portalPresentation.js', () => ({
  gradients: [],
  iconMap: {},
}));

vi.mock('../api/channels.js', () => ({
  requestTypes: { delete: vi.fn() },
  assetReports: { delete: vi.fn() },
}));

vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: { load: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../stores/portalAuth.svelte.js', () => ({
  portalAuthStore: readable({ user: null }),
}));

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: readable({ currentUser: null }),
}));

vi.mock('../stores/permissionProfile.js', () => ({
  loadPermissionProfile: vi
    .fn()
    .mockResolvedValue({ has_system_admin: false, workspace_permissions: {} }),
}));

vi.mock('../composables/useConfirm.js', () => ({
  confirm: vi.fn(),
}));

vi.mock('../stores/portal.svelte.js', () => {
  const portalCustomizationStore = {
    activeSection: 'request-types',
    isEditing: false,
    isDarkMode: false,
    showCustomizePanel: true,
    portalData: { channel_id: 7, workspace_ids: [] },
    knowledgeBasePageSources: [],
    knowledgeBaseShareLink: '',
    backgroundImageUrl: null,
    hasBackgroundImage: false,
    selectedBackgroundCategory: 'gradients',
    selectedGradient: 0,
    logoUrl: null,
    uploadingBackground: false,
    uploadingLogo: false,
    addKnowledgeBasePageSource: vi.fn(),
    handleBackgroundUpload: vi.fn(),
    handleLogoUpload: vi.fn(),
    parseDocmostShareLink: vi.fn(),
    removeBackgroundImage: vi.fn(),
    removeKnowledgeBasePageSource: vi.fn(),
    removeLogo: vi.fn(),
    saveKnowledgeBaseConfig: vi.fn(),
    selectBackgroundImage: vi.fn(),
    selectGradient: vi.fn(),
    toggleEditing: vi.fn(),
  };
  const portalCatalogStore = {
    get requestTypes() {
      return mocks.requestTypes;
    },
    get assetReports() {
      return mocks.assetReports;
    },
    loadingRequestTypes: false,
    loadingAssetReports: false,
    hasAssetSets: false,
    draggedRequestType: null,
    draggedAssetReport: null,
    loadRequestTypes: vi.fn(),
    loadAssetReports: vi.fn(),
  };
  return { portalCustomizationStore, portalCatalogStore };
});

import PortalCustomizePanel from './PortalCustomizePanel.svelte';
import { assetReports, requestTypes } from '../api/channels.js';

describe('PortalCustomizePanel system request types (WI-1644)', () => {
  beforeEach(() => {
    mocks.assetReports = [];
    mocks.requestTypes = [
      {
        id: 1,
        name: 'Email',
        kind: 'email',
        field_count: 0,
        item_type_name: 'Ticket',
        icon: 'Mail',
        color: '#6b7280',
      },
      {
        id: 2,
        name: 'Bug report',
        kind: '',
        field_count: 2,
        item_type_name: 'Bug',
        icon: 'Bug',
        color: '#3b82f6',
      },
    ];
  });

  it('badges the system row, locks its fields, and removes delete from its actions', async () => {
    render(PortalCustomizePanel, { props: { onOpenRequestTypeModal: vi.fn() } });

    expect(screen.getByTestId('request-type-system-badge-1')).toHaveTextContent(
      'portal.customize.systemRequestType'
    );
    expect(screen.queryByTestId('request-type-system-badge-2')).not.toBeInTheDocument();

    // The system row explains itself and offers no field editing.
    const systemCard = document.querySelector('[data-request-type-id="1"]');
    expect(within(systemCard).getByText('portal.customize.systemRequestTypeHelp')).toBeInTheDocument();
    expect(within(systemCard).queryByText('portal.customize.addFields')).not.toBeInTheDocument();
    expect(within(systemCard).queryByText(/portal\.customize\.fields/)).not.toBeInTheDocument();

    // A normal row keeps field editing.
    const normalCard = document.querySelector('[data-request-type-id="2"]');
    expect(within(normalCard).getByText(/portal\.customize\.fields/)).toBeInTheDocument();

    // System row actions: edit only.
    await fireEvent.click(screen.getByTestId('request-type-actions-1'));
    expect(screen.getByText('common.edit')).toBeInTheDocument();
    expect(screen.queryByText('common.delete')).not.toBeInTheDocument();

    // A normal row keeps field editing and delete.
    await fireEvent.click(screen.getByTestId('request-type-actions-2'));
    expect(screen.getByText('common.delete')).toBeInTheDocument();
  });
});
