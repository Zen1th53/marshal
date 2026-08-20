import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ToastProvider, useToast, ToastContainer } from './index';

function TestToastTrigger() {
  const { addToast } = useToast();
  return (
    <div>
      <button onClick={() => addToast({ type: 'info', message: 'Test message 1' })}>Add Toast 1</button>
      <button onClick={() => addToast({ type: 'error', message: 'Test error', correlationId: 'corr-001' })}>Add Error Toast</button>
      <button onClick={() => {
        for (let i = 1; i <= 8; i++) {
          addToast({ type: 'info', message: `Batch Toast ${i}` }, 0);
        }
      }}>Add 8 Toasts</button>
    </div>
  );
}

describe('Toast System (T172)', () => {
  it('renders and dismisses toast notifications', async () => {
    render(
      <ToastProvider>
        <TestToastTrigger />
        <ToastContainer />
      </ToastProvider>
    );

    await userEvent.click(screen.getByRole('button', { name: 'Add Toast 1' }));
    expect(screen.getByText('Test message 1')).toBeInTheDocument();

    const closeBtn = screen.getByRole('button', { name: 'Dismiss notification' });
    await userEvent.click(closeBtn);
    expect(screen.queryByText('Test message 1')).toBeNull();
  });

  it('renders correlation ID for error toast', async () => {
    render(
      <ToastProvider>
        <TestToastTrigger />
        <ToastContainer />
      </ToastProvider>
    );

    await userEvent.click(screen.getByRole('button', { name: 'Add Error Toast' }));
    expect(screen.getByText('Test error')).toBeInTheDocument();
    expect(screen.getByText('[corr-001]')).toBeInTheDocument();
  });

  it('STRICT INVARIANT: Bounded toast queue (max 5 active items)', async () => {
    render(
      <ToastProvider>
        <TestToastTrigger />
        <ToastContainer />
      </ToastProvider>
    );

    await userEvent.click(screen.getByRole('button', { name: 'Add 8 Toasts' }));
    const activeAlerts = screen.getAllByRole('alert');
    // Bounded to 5 items maximum!
    expect(activeAlerts.length).toBe(5);
    // Oldest items (1, 2, 3) must be evicted, newest (4..8) present
    expect(screen.queryByText('Batch Toast 1')).toBeNull();
    expect(screen.getByText('Batch Toast 8')).toBeInTheDocument();
  });
});
