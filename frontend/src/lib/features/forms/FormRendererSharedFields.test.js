import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: { forms: { submit: vi.fn() } },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../runtime/contextPath.js', () => ({
  toExternal: (path) => path,
}));

const { default: FormRenderer } = await import('./FormRenderer.svelte');

describe('FormRenderer shared fields and navigation', () => {
  afterEach(() => {
    vi.useRealTimers();
    localStorage.clear();
    sessionStorage.clear();
  });

  it('does not create or announce a draft until the user changes the form', async () => {
    vi.useFakeTimers();
    localStorage.clear();
    sessionStorage.clear();

    render(FormRenderer, {
      props: {
        formSlug: 'support',
        formId: 7,
        initialDetail: {
          form_id: 7,
          fields: [
            {
              field_identifier: 'title',
              field_type: 'default',
              display_name: 'Summary',
              is_required: true,
              step_number: 1,
            },
          ],
          custom_field_definitions: [],
        },
      },
    });

    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(350);
    expect(screen.queryByTestId('public-form-draft-status')).toBeNull();
    // Anonymous drafts persist to sessionStorage, never localStorage.
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);

    await fireEvent.input(screen.getByLabelText(/^Summary/), {
      target: { value: 'Printer issue' },
    });
    expect(screen.getByTestId('public-form-draft-status').textContent).toContain('Saving draft');

    await vi.advanceTimersByTimeAsync(350);
    expect(screen.getByTestId('public-form-draft-status').textContent).toContain('Draft saved');
    expect(sessionStorage.length).toBe(1);
    expect(localStorage.length).toBe(0);
  });

  it('validates and submits default and virtual fields across steps', async () => {
    const submitForm = vi.fn().mockResolvedValue({ success: true });
    render(FormRenderer, {
      props: {
        formSlug: 'support',
        formId: 7,
        initialDetail: {
          form_id: 7,
          fields: [
            {
              field_identifier: 'title',
              field_type: 'default',
              display_name: 'Summary',
              is_required: true,
              step_number: 1,
            },
            {
              field_identifier: 'description',
              field_type: 'default',
              display_name: 'Details',
              is_required: true,
              step_number: 2,
            },
            {
              field_identifier: 'confirmed',
              field_type: 'virtual',
              virtual_field_type: 'checkbox',
              display_name: 'I confirm',
              is_required: true,
              step_number: 2,
            },
          ],
          custom_field_definitions: [],
        },
        submitForm,
        preview: true,
      },
    });

    const submitButton = await screen.findByTestId('public-form-submit');
    await fireEvent.submit(submitButton.closest('form'));
    expect(await screen.findByText('Summary is required')).toBeTruthy();

    await fireEvent.input(screen.getByLabelText(/^Summary/), {
      target: { value: 'Printer issue' },
    });
    await fireEvent.click(screen.getByTestId('public-form-submit'));
    expect(await screen.findByLabelText(/^Details/)).toBeTruthy();
    expect(screen.getByLabelText(/^I confirm/)).toBeTruthy();
    expect(screen.getByTestId('public-form-submit').textContent).toContain('Preview only');
    expect(submitForm).not.toHaveBeenCalled();
  });
});
