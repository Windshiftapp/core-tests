import { beforeEach, describe, expect, it, vi } from 'vitest';
import { forms } from '../../api/forms.js';


vi.mock('../../api/forms.js', () => ({
  forms: {
        getBootstrap: vi.fn(),
        getFormDetail: vi.fn(),
        getChannel: vi.fn(),
        getForms: vi.fn(),
        getFormFields: vi.fn(),
        getCustomFields: vi.fn(),
      },
}));


const { loadPublicFormBootstrap, loadPublicFormDetail } = await import('./publicFormData.js');

describe('public form request graph', () => {
  beforeEach(() => vi.clearAllMocks());

  it('loads a sole-form cold entry with one bootstrap request', async () => {
    const response = {
      channel: { name: 'Support' },
      forms: [{ id: 7 }],
      form_detail: { form_id: 7, fields: [], custom_field_definitions: [] },
    };
    forms.getBootstrap.mockResolvedValue(response);

    await expect(loadPublicFormBootstrap('support')).resolves.toBe(response);

    expect(forms.getBootstrap).toHaveBeenCalledOnce();
    expect(forms.getBootstrap).toHaveBeenCalledWith('support');
    expect(forms.getChannel).not.toHaveBeenCalled();
    expect(forms.getForms).not.toHaveBeenCalled();
    expect(forms.getFormFields).not.toHaveBeenCalled();
    expect(forms.getCustomFields).not.toHaveBeenCalled();
  });

  it('loads a selected form with one complete-detail request', async () => {
    const response = { form_id: 8, fields: [], custom_field_definitions: [] };
    forms.getFormDetail.mockResolvedValue(response);

    await expect(loadPublicFormDetail('support', 8)).resolves.toBe(response);

    expect(forms.getFormDetail).toHaveBeenCalledOnce();
    expect(forms.getFormDetail).toHaveBeenCalledWith('support', 8);
    expect(forms.getFormFields).not.toHaveBeenCalled();
    expect(forms.getCustomFields).not.toHaveBeenCalled();
  });
});
