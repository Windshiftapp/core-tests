import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { addToast, removeToast, toasts } from '../../stores/toasts.svelte.js';
import ToastContainer from './ToastContainer.svelte';

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

afterEach(() => {
  for (const toast of [...toasts.value]) removeToast(toast.id);
});

describe('ToastContainer mobile layout', () => {
  it('keeps the toast stack within the viewport and below the safe area', async () => {
    render(ToastContainer);
    addToast({ message: 'A new version is ready.', duration: 0 });

    const viewport = await screen.findByTestId('toast-viewport');
    const toast = screen.getByTestId('toast');

    expect(viewport.getAttribute('style')).toContain('safe-area-inset-top');
    expect(viewport.getAttribute('style')).toContain('safe-area-inset-left');
    expect(viewport.getAttribute('style')).toContain('safe-area-inset-right');
    expect(toast).toHaveClass('w-full');
    expect(toast).not.toHaveClass('w-[360px]');
  });
});
