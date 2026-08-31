import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

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

describe('FormRenderer authenticated submission recovery', () => {
  it('shows an actionable sign-in state after an authenticated form rejects an anonymous submit', async () => {
    const submitForm = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error('Forbidden'), { status: 403 }));
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
              field_label: 'Title',
              is_required: true,
              step_number: 1,
            },
          ],
          custom_field_definitions: [],
        },
        submitForm,
      },
    });

    const title = await screen.findByLabelText(/^requestForm\.title/);
    await fireEvent.input(title, { target: { value: 'Printer request' } });
    await fireEvent.click(screen.getByTestId('public-form-submit'));

    expect(await screen.findByTestId('form-auth-required')).toBeTruthy();
    expect(screen.getByRole('link').getAttribute('href')).toBe('/login?return_to=%2F');
    expect(submitForm).toHaveBeenCalledOnce();
  });

  it('gates an authenticated form before the user enters data', async () => {
    render(FormRenderer, {
      props: {
        formSlug: 'support',
        formId: 7,
        authenticationRequired: true,
        initialDetail: {
          form_id: 7,
          fields: [],
          custom_field_definitions: [],
        },
      },
    });

    expect(await screen.findByTestId('form-auth-required')).toBeTruthy();
    expect(screen.queryByTestId('public-form-submit')).toBeNull();
  });
});
