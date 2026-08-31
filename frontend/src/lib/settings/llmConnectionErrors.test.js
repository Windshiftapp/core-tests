import { describe, expect, it } from 'vitest';

import { llmConnectionTestErrorMessage } from './llmConnectionErrors.js';

describe('llmConnectionTestErrorMessage', () => {
  it('extracts an OpenAI-compatible provider message from the API wrapper', () => {
    const providerMessage =
      'litellm.BadRequestError: Could not finish the message because max_tokens was reached.';
    const error = new Error(
      `Connection test failed: failed to connect to LLM service: LLM API error: status 400 - ${JSON.stringify(
        {
          error: {
            message: providerMessage,
            type: 'invalid_request_error',
          },
        }
      )}`
    );

    expect(llmConnectionTestErrorMessage(error)).toBe(providerMessage);
  });

  it('extracts a provider message from an array response', () => {
    const error = new Error(
      'Connection test failed: LLM API error: status 404 - ' +
        JSON.stringify([{ error: { message: 'The selected model is no longer available' } }])
    );

    expect(llmConnectionTestErrorMessage(error)).toBe('The selected model is no longer available');
  });

  it('keeps actionable network errors and supplies a fallback for empty failures', () => {
    expect(
      llmConnectionTestErrorMessage(
        new Error('Unable to connect to the server. Check your connection and try again.')
      )
    ).toBe('Unable to connect to the server. Check your connection and try again.');
    expect(llmConnectionTestErrorMessage({})).toBe('Connection test failed');
  });
});
