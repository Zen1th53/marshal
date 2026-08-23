import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { MarshalPet } from './MarshalPet';
import { PetRenderer } from './PetRenderer';
import { PetSpeechBubble } from './PetSpeechBubble';
import { PetPopover } from './PetPopover';

describe('MarshalPet component suite', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders PetRenderer in various states', () => {
    const { rerender, container } = render(<PetRenderer state="idle" />);
    expect(container.querySelector('svg')).toBeTruthy();

    rerender(<PetRenderer state="working" />);
    expect(container.querySelector('.pet-eye-glow')).toBeTruthy();

    rerender(<PetRenderer state="success" />);
    expect(container.querySelector('svg')).toBeTruthy();

    rerender(<PetRenderer state="sleeping" />);
    expect(container.querySelector('svg')).toBeTruthy();

    rerender(<PetRenderer state="warning" />);
    expect(container.querySelector('svg')).toBeTruthy();
  });

  it('renders speech bubble and triggers dismiss and action', () => {
    const onDismiss = vi.fn();
    const onAction = vi.fn();

    render(
      <PetSpeechBubble
        message={{
          id: 'test-msg',
          text: 'Security finding in sandbox.',
          priority: 'warning',
          action: { label: 'View Security', route: 'security' },
          createdAt: Date.now(),
        }}
        onDismiss={onDismiss}
        onAction={onAction}
      />
    );

    expect(screen.getByText('Security finding in sandbox.')).toBeTruthy();
    expect(screen.getByText('View Security →')).toBeTruthy();

    fireEvent.click(screen.getByText('View Security →'));
    expect(onAction).toHaveBeenCalledWith('security', undefined);

    fireEvent.click(screen.getByLabelText('Dismiss message'));
    expect(onDismiss).toHaveBeenCalled();
  });

  it('renders PetPopover and handles menu clicks', () => {
    const onClose = vi.fn();
    const onStateChange = vi.fn();
    const onNavigate = vi.fn();
    const onResetPosition = vi.fn();

    render(
      <PetPopover
        isOpen={true}
        onClose={onClose}
        state="idle"
        onStateChange={onStateChange}
        onNavigate={onNavigate}
        onResetPosition={onResetPosition}
      />
    );

    expect(screen.getByText('MARSHAL Companion')).toBeTruthy();
    expect(screen.getByText('Tasks & Workflows')).toBeTruthy();

    fireEvent.click(screen.getByText('Tasks & Workflows'));
    expect(onNavigate).toHaveBeenCalledWith('tasks');
    expect(onClose).toHaveBeenCalled();

    fireEvent.click(screen.getByText('Go to Sleep'));
    expect(onStateChange).toHaveBeenCalledWith('sleeping');
  });

  it('renders MarshalPet overlay and allows opening popover', () => {
    render(<MarshalPet />);
    const petContainer = screen.getByRole('button', { name: /MARSHAL Assistant Mascot/i });
    expect(petContainer).toBeTruthy();

    // Click without drag opens popover
    fireEvent.click(petContainer);

    expect(screen.getByText('MARSHAL Companion')).toBeTruthy();
  });
});
