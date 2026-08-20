import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CommandPalette } from './CommandPalette';

describe('CommandPalette (T180)', () => {
  it('filters routes by search query and navigates on Enter', async () => {
    const onNavigate = vi.fn();
    const onClose = vi.fn();

    render(
      <CommandPalette
        isOpen={true}
        onClose={onClose}
        onNavigate={onNavigate}
      />
    );

    const input = screen.getByPlaceholderText(/Type a command/i);
    await userEvent.type(input, 'audit');

    expect(screen.getByText('Audit Log')).toBeInTheDocument();
    expect(screen.queryByText('Overview')).toBeNull();

    await userEvent.keyboard('{Enter}');
    expect(onNavigate).toHaveBeenCalledWith('audit');
    expect(onClose).toHaveBeenCalled();
  });

  it('closes on Escape key', async () => {
    const onClose = vi.fn();

    render(
      <CommandPalette
        isOpen={true}
        onClose={onClose}
        onNavigate={vi.fn()}
      />
    );

    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });
});
