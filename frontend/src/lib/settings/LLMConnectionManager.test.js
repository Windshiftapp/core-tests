import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/ai.js', () => ({
  llmConnections: {
        getAll: vi.fn(),
        test: vi.fn(),
        update: vi.fn(),
        create: vi.fn(),
      },
  llmProviders: {
        getProviders: vi.fn(),
      },
  actionCapabilities: {
        getAll: vi.fn(),
      },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../stores/i18n.svelte.js', async (importOriginal) => ({
  ...(await importOriginal()),
  t: (key) =>
    key === 'settings.adminOperations.llmConnections.testFailed' ? 'Connection test failed' : key,
}));

vi.mock('../composables/useConfirm.js', () => ({
  confirm: vi.fn(),
}));

import { actionCapabilities, llmConnections, llmProviders } from '../api/ai.js';
import { errorToast } from '../stores/toasts.svelte.js';
import LLMConnectionManager from './LLMConnectionManager.svelte';

const connection = {
  id: 7,
  name: 'LLM proxy',
  provider_type: 'local',
  model: 'openai/gpt-5.3-chat-latest',
  base_url: 'http://litellm:4000',
  provider_config: '',
  is_default: true,
  is_enabled: true,
};

describe('LLMConnectionManager connection test errors', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    llmConnections.getAll.mockResolvedValue([connection]);
    llmProviders.getProviders.mockResolvedValue([
      {
        type: 'local',
        name: 'Local / Custom',
        models: [{ id: connection.model, name: connection.model }],
      },
    ]);
    actionCapabilities.getAll.mockResolvedValue([]);
  });

  it('shows only the provider message in an error toast and keeps it out of the modal', async () => {
    const providerMessage = 'Could not finish the message because max_tokens was reached.';
    llmConnections.test.mockRejectedValue(
      new Error(
        `Connection test failed: failed to connect to LLM service: LLM API error: status 400 - ${JSON.stringify(
          { error: { message: providerMessage, type: 'invalid_request_error' } }
        )}`
      )
    );

    render(LLMConnectionManager);

    await fireEvent.click(await screen.findByTestId('llm-connection-edit'));
    await fireEvent.click(screen.getByTestId('llm-connection-test-modal'));

    await waitFor(() => {
      expect(errorToast).toHaveBeenCalledWith(providerMessage, 'Connection test failed');
    });
    expect(screen.queryByText(providerMessage)).not.toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
});

describe('LLMConnectionManager provider guidance', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    llmConnections.getAll.mockResolvedValue([
      {
        ...connection,
        id: 9,
        name: 'OpenCode Go',
        provider_type: 'opencode-go',
        model: 'kimi-k2.7-code',
        base_url: '',
      },
    ]);
    llmProviders.getProviders.mockResolvedValue([
      {
        type: 'opencode-go',
        name: 'OpenCode Go',
        models_endpoint: '/v1/models',
        usage_note: 'OpenCode Go is intended for coding agents.',
        models: [],
      },
    ]);
    actionCapabilities.getAll.mockResolvedValue([]);
  });

  it('renders the provider usage guidance in the connection form', async () => {
    render(LLMConnectionManager);

    await fireEvent.click(await screen.findByTestId('llm-connection-edit'));

    const note = await screen.findByTestId('llm-provider-usage-note');
    expect(note).toHaveTextContent('OpenCode Go is intended for coding agents.');
  });
});

describe('LLMConnectionManager max output tokens', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    llmConnections.getAll.mockResolvedValue([connection]);
    llmConnections.update.mockResolvedValue({});
    llmProviders.getProviders.mockResolvedValue([
      {
        type: 'local',
        name: 'Local / Custom',
        models: [{ id: connection.model, name: connection.model }],
      },
    ]);
    actionCapabilities.getAll.mockResolvedValue([]);
  });

  it('saves the max output tokens override into provider_config', async () => {
    render(LLMConnectionManager);

    await fireEvent.click(await screen.findByTestId('llm-connection-edit'));
    const input = document.getElementById('llm-connection-max-output-tokens');
    expect(input).not.toBeNull();
    await fireEvent.input(input, { target: { value: '65536' } });
    await fireEvent.click(screen.getByTestId('entity-form-confirm'));

    await waitFor(() => {
      expect(llmConnections.update).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ provider_config: JSON.stringify({ max_output_tokens: 65536 }) })
      );
    });
  });

  it('does not forward an empty override', async () => {
    render(LLMConnectionManager);

    await fireEvent.click(await screen.findByTestId('llm-connection-edit'));
    const input = document.getElementById('llm-connection-max-output-tokens');
    await fireEvent.input(input, { target: { value: '' } });
    await fireEvent.click(screen.getByTestId('entity-form-confirm'));

    await waitFor(() => {
      expect(llmConnections.update).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ provider_config: '' })
      );
    });
  });
});
