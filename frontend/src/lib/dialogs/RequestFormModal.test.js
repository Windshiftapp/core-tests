import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { portal } from '../api/portal.js';
import RequestFormModal from './RequestFormModal.svelte';

const authState = vi.hoisted(() => ({ value: { isAuthenticated: false } }));

vi.mock('../api/portal.js', () => ({
  portal: {
        getRequestTypeFields: vi.fn(),
        getCustomFields: vi.fn(),
        submit: vi.fn(),
        drafts: { getForRequestType: vi.fn(), save: vi.fn(), delete: vi.fn() },
      },
}));
vi.mock('../stores/auth.svelte.js', () => ({
  authStore: { isAuthenticated: false },
}));
vi.mock('../stores/portalAuth.svelte.js', () => ({
  portalAuthStore: {
    subscribe: (run) => {
      run(authState.value);
      return () => {};
    },
  },
}));
vi.mock('../stores/portal.svelte.js', () => ({ portalCustomizationStore: {} }));
vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

beforeEach(() => {
  authState.value = { isAuthenticated: false };
  portal.submit.mockResolvedValue({ item_id: 99 });
  portal.getRequestTypeFields.mockResolvedValue([
    { field_identifier: 'title', field_type: 'default', display_name: 'Summary', step_number: 1 },
    {
      field_identifier: 'description',
      field_type: 'default',
      display_name: 'Details',
      step_number: 2,
    },
  ]);
  portal.getCustomFields.mockResolvedValue([]);
  portal.drafts.getForRequestType.mockResolvedValue(null);
  portal.drafts.save.mockImplementation(async (_slug, payload) => ({ id: 1, ...payload }));
  portal.drafts.delete.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function openForm() {
  return render(RequestFormModal, {
    isOpen: true,
    portalSlug: 'support',
    requestType: { id: 7, name: 'Incident' },
  });
}

it('saves a new request on step two without announcing a resumed draft', async () => {
  openForm();
  await fireEvent.input(await screen.findByLabelText(/^Summary/), {
    target: { value: 'Printer issue' },
  });
  await fireEvent.click(screen.getByTestId('request-form-next-step'));
  expect(await screen.findByText('portal.draftSaved')).toBeTruthy();
  expect(portal.drafts.save).toHaveBeenCalledWith('support', {
    request_type_id: 7,
    title: 'Printer issue',
    description: '',
    custom_fields: {},
    current_step: 2,
  });
  expect(screen.queryByTestId('request-form-draft-resume-banner')).toBeNull();
  expect(screen.getByLabelText(/^Details/)).toBeTruthy();
});

it('offers the organisation share toggle only in requester_choice mode', async () => {
  authState.value = {
    isAuthenticated: true,
    customer: { name: 'Jane', email: 'jane@example.com' },
    userBootstrap: { request_sharing: 'requester_choice' },
  };
  openForm();
  await fireEvent.input(await screen.findByLabelText(/^Summary/), {
    target: { value: 'Printer issue' },
  });
  await fireEvent.click(screen.getByTestId('request-form-next-step'));

  const toggle = await screen.findByTestId('request-form-share-with-organisation');
  await fireEvent.click(toggle);
  await fireEvent.click(screen.getByTestId('request-form-submit'));

  await waitFor(() => {
    expect(portal.submit).toHaveBeenCalledWith(
      'support',
      expect.objectContaining({ share_with_organisation: true })
    );
  });
});

it('hides the share toggle when the organisation shares automatically', async () => {
  authState.value = {
    isAuthenticated: true,
    customer: { name: 'Jane', email: 'jane@example.com' },
    userBootstrap: { request_sharing: 'automatic' },
  };
  openForm();
  await fireEvent.input(await screen.findByLabelText(/^Summary/), {
    target: { value: 'Printer issue' },
  });
  await fireEvent.click(screen.getByTestId('request-form-next-step'));

  expect(screen.queryByTestId('request-form-share-with-organisation')).toBeNull();
});

it('restores the saved step and title when reopening a request', async () => {
  portal.drafts.getForRequestType.mockResolvedValue({ title: 'Saved title', current_step: 2 });
  openForm();
  expect(await screen.findByTestId('request-form-draft-resume-banner')).toBeTruthy();
  expect(screen.getByLabelText(/^Details/)).toBeTruthy();
  await fireEvent.click(screen.getByTestId('request-form-back-step'));
  expect(screen.getByLabelText(/^Summary/).value).toBe('Saved title');
});

it('keeps the resume notice dismissed after starting fresh and advancing', async () => {
  portal.drafts.getForRequestType.mockResolvedValue({ title: 'Saved title', current_step: 2 });
  openForm();
  await fireEvent.click(await screen.findByText('portal.draftStartFresh'));
  expect((await screen.findByLabelText(/^Summary/)).value).toBe('');
  expect(portal.drafts.delete).toHaveBeenCalledWith('support', 7);
  await fireEvent.input(screen.getByLabelText(/^Summary/), { target: { value: 'Fresh title' } });
  await fireEvent.click(screen.getByTestId('request-form-next-step'));
  expect(await screen.findByText('portal.draftSaved')).toBeTruthy();
  expect(screen.queryByTestId('request-form-draft-resume-banner')).toBeNull();
});

it('reapplies route prefill after starting fresh from a draft (WI-1590)', async () => {
  portal.getRequestTypeFields.mockResolvedValue([
    { field_identifier: 'title', field_type: 'default', display_name: 'Summary', step_number: 1 },
    { field_identifier: '42', field_type: 'custom', display_name: 'Device', step_number: 1 },
  ]);
  portal.getCustomFields.mockResolvedValue([{ id: 42, name: 'Device', field_type: 'text' }]);
  portal.drafts.getForRequestType.mockResolvedValue({
    title: 'Draft title',
    custom_field_values: { 42: 'draft-device' },
    current_step: 1,
  });

  render(RequestFormModal, {
    isOpen: true,
    portalSlug: 'support',
    requestType: { id: 7, name: 'Incident' },
    prefill: { 42: '431' },
  });

  expect(await screen.findByDisplayValue('431')).toBeInTheDocument();
  await fireEvent.click(await screen.findByText('portal.draftStartFresh'));

  // Starting fresh discards the draft but keeps the route's clicked asset.
  expect((await screen.findByLabelText(/^Summary/)).value).toBe('');
  expect(await screen.findByDisplayValue('431')).toBeInTheDocument();
});

it('applies prefill after a resumed draft and ignores unknown keys', async () => {
  portal.getRequestTypeFields.mockResolvedValue([
    { field_identifier: 'title', field_type: 'default', display_name: 'Summary', step_number: 1 },
    { field_identifier: '42', field_type: 'custom', display_name: 'Device', step_number: 1 },
  ]);
  portal.getCustomFields.mockResolvedValue([{ id: 42, name: 'Device', field_type: 'text' }]);
  portal.drafts.getForRequestType.mockResolvedValue({
    title: 'Draft title',
    custom_field_values: { 42: 'old-device' },
    current_step: 1,
  });

  render(RequestFormModal, {
    isOpen: true,
    portalSlug: 'support',
    requestType: { id: 7, name: 'Incident' },
    prefill: { 42: '431', unknown: 'ignored' },
  });

  // The draft's other values survive while the prefilled target field wins.
  expect((await screen.findByLabelText(/^Summary/)).value).toBe('Draft title');
  expect(await screen.findByDisplayValue('431')).toBeInTheDocument();
  expect(screen.queryByDisplayValue('ignored')).toBeNull();
});
