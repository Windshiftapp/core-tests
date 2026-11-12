import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  upload: vi.fn(),
  errorToast: vi.fn(),
  warningToast: vi.fn(),
}));

vi.mock('../api/misc.js', () => ({ attachments: { upload: mocks.upload } }));
vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  errorToast: mocks.errorToast,
  warningToast: mocks.warningToast,
}));

import TimeCustomerModal from './TimeCustomerModal.svelte';

function formData() {
  return {
    name: 'Acme',
    email: '',
    description: '',
    active: true,
    avatar_url: null,
    custom_field_values: {},
  };
}

async function uploadAvatar(props) {
  render(TimeCustomerModal, {
    props: { isOpen: true, formData: formData(), customerOrgFields: [], attachmentsEnabled: true, ...props },
  });
  await fireEvent.click(screen.getByText('organization.uploadAvatar'));
  const file = new File(['avatar'], 'avatar.png', { type: 'image/png' });
  await fireEvent.change(document.querySelector('input[type="file"]'), {
    target: { files: [file] },
  });
  await waitFor(() => expect(mocks.upload).toHaveBeenCalledTimes(1));
  return mocks.upload.mock.calls[0][0];
}

describe('TimeCustomerModal avatar upload', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.upload.mockResolvedValue({ success: true, avatar_url: '/api/attachments/1/download' });
  });

  afterEach(cleanup);

  it('stages the upload with entity_id 0 when creating an organisation', async () => {
    const body = await uploadAvatar({ entityId: 0, isEditing: false });

    expect(body.get('category')).toBe('customer_avatar');
    expect(body.get('entity_id')).toBe('0');
  });

  it('scopes the upload to the edited organisation', async () => {
    const body = await uploadAvatar({ entityId: 7, isEditing: true });

    expect(body.get('category')).toBe('customer_avatar');
    expect(body.get('entity_id')).toBe('7');
  });
});

describe('TimeCustomerModal custom fields', () => {
  afterEach(cleanup);

  it('shows each custom field under its own label', () => {
    render(TimeCustomerModal, {
      props: {
        isOpen: true,
        formData: formData(),
        attachmentsEnabled: false,
        customerOrgFields: [
          { id: 11, name: 'Account Tier', field_type: 'text', required: true },
        ],
      },
    });

    const label = screen.getByText(/Account Tier/);
    expect(label.tagName).toBe('LABEL');
    expect(label.querySelector('span')).toHaveTextContent('*');
    expect(screen.getByTestId('custom-field-input-11')).toBeInTheDocument();
  });
});
