import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    forms: {
      getBootstrap: vi.fn(),
      getFormDetail: vi.fn(),
      getChannel: vi.fn(),
      getForms: vi.fn(),
      getFormFields: vi.fn(),
      getCustomFields: vi.fn(),
    },
  },
}));

const { api } = await import('../../api.js');
const { loadPublicFormBootstrap, loadPublicFormDetail } = await import('./publicFormData.js');

describe('public form request graph', () => {
  beforeEach(() => vi.clearAllMocks());

  it('loads a sole-form cold entry with one bootstrap request', async () => {
    const response = {
      channel: { name: 'Support' },
      forms: [{ id: 7 }],
      form_detail: { form_id: 7, fields: [], custom_field_definitions: [] },
    };
    api.forms.getBootstrap.mockResolvedValue(response);

    await expect(loadPublicFormBootstrap('support')).resolves.toBe(response);

    expect(api.forms.getBootstrap).toHaveBeenCalledOnce();
    expect(api.forms.getBootstrap).toHaveBeenCalledWith('support');
    expect(api.forms.getChannel).not.toHaveBeenCalled();
    expect(api.forms.getForms).not.toHaveBeenCalled();
    expect(api.forms.getFormFields).not.toHaveBeenCalled();
    expect(api.forms.getCustomFields).not.toHaveBeenCalled();
  });

  it('loads a selected form with one complete-detail request', async () => {
    const response = { form_id: 8, fields: [], custom_field_definitions: [] };
    api.forms.getFormDetail.mockResolvedValue(response);

    await expect(loadPublicFormDetail('support', 8)).resolves.toBe(response);

    expect(api.forms.getFormDetail).toHaveBeenCalledOnce();
    expect(api.forms.getFormDetail).toHaveBeenCalledWith('support', 8);
    expect(api.forms.getFormFields).not.toHaveBeenCalled();
    expect(api.forms.getCustomFields).not.toHaveBeenCalled();
  });
});
