import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import ChatToolTrace from './ChatToolTrace.svelte';

afterEach(cleanup);

describe('ChatToolTrace', () => {
  it('warns when the model hits its output limit', () => {
    render(ChatToolTrace, {
      props: {
        toolCalls: [{ name: 'get_item', arguments: '{}', result: '{}' }],
        iterations: 3,
        maxIterations: 12,
        stopReason: 'max_tokens',
      },
    });

    expect(
      screen.getByText(/The model hit its output limit — the answer is truncated/)
    ).toBeInTheDocument();
  });

  it('warns when the loop stopped making progress', () => {
    render(ChatToolTrace, {
      props: {
        toolCalls: [],
        iterations: 4,
        maxIterations: 12,
        stopReason: 'no_progress',
      },
    });

    expect(
      screen.getByText(/Stopped: the model kept repeating the same steps/)
    ).toBeInTheDocument();
  });

  it('shows the plain step count for a clean run', () => {
    render(ChatToolTrace, {
      props: {
        toolCalls: [{ name: 'get_item', arguments: '{}', result: '{}' }],
        iterations: 3,
        maxIterations: 12,
        stopReason: 'done',
      },
    });

    expect(screen.getByText('1 tool call · 3/12 steps')).toBeInTheDocument();
  });
});
