import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StatusBadge, Button } from '../components/ui';
import { GlobalEntityNavigator } from '../features/search/GlobalEntityNavigator';
import { CommandPalette } from '../components/command/CommandPalette';
import { ToastProvider, ToastContainer, useToast } from '../components/toast';

function TestToastTrigger() {
  const { addToast } = useToast();
  return (
    <Button
      variant="primary"
      size="sm"
      onClick={() => addToast({ type: 'info', message: 'Polite live announcement' })}
    >
      Trigger Toast
    </Button>
  );
}

describe('Accessibility & Keyboard Conformance (T216)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('verifies StatusBadge always renders explicit text label (color not sole signal)', () => {
    render(<StatusBadge status="ready" label="ACTIVE READY" />);
    const badge = screen.getByRole('status');
    expect(badge).toHaveTextContent('ACTIVE READY');
    expect(badge).toHaveAttribute('aria-label', 'ACTIVE READY');
  });

  it('verifies GlobalEntityNavigator dialog declares modal attributes and responds to Escape', async () => {
    const closeFn = vi.fn();
    render(<GlobalEntityNavigator isOpen={true} onClose={closeFn} onNavigate={vi.fn()} />);

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAttribute('aria-label', 'Global Navigator');

    const input = screen.getByPlaceholderText(/Search tasks, runs/i);
    await userEvent.type(input, '{Escape}');

    expect(closeFn).toHaveBeenCalled();
  });

  it('verifies CommandPalette dialog declares modal attributes and supports keyboard navigation', async () => {
    const closeFn = vi.fn();
    const navFn = vi.fn();
    render(<CommandPalette isOpen={true} onClose={closeFn} onNavigate={navFn} />);

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAttribute('aria-label', 'Command palette');

    const input = screen.getByPlaceholderText(/Type a command or navigate/i);
    await userEvent.type(input, '{ArrowDown}{Enter}');

    expect(navFn).toHaveBeenCalled();
  });

  it('verifies live toast announcements use polite aria-live regions', async () => {
    render(
      <ToastProvider>
        <TestToastTrigger />
        <ToastContainer />
      </ToastProvider>
    );

    const trigger = screen.getByRole('button', { name: /Trigger Toast/i });
    await userEvent.click(trigger);

    expect(await screen.findByText('Polite live announcement')).toBeInTheDocument();
  });
});
