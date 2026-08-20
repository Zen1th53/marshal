import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { LoadingState, EmptyState, ErrorState } from './index';

describe('StateViews (T172)', () => {
  it('LoadingState renders live region with aria-busy', () => {
    render(<LoadingState message="Fetching system telemetry…" />);
    const status = screen.getByRole('status');
    expect(status).toHaveAttribute('aria-busy', 'true');
    expect(status).toHaveTextContent('Fetching system telemetry…');
  });

  it('EmptyState renders title, description, and action button', () => {
    render(
      <EmptyState
        title="No Tasks Found"
        description="No tasks match the active risk filter."
        action={<button>Create Task</button>}
      />
    );
    expect(screen.getByRole('region')).toHaveAccessibleName('No Tasks Found');
    expect(screen.getByText('No tasks match the active risk filter.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create Task' })).toBeInTheDocument();
  });

  it('ErrorState renders safe unauthorized message without leaking data counts', () => {
    render(
      <ErrorState
        severity="unauthorized"
        correlationId="req-999-authz"
      />
    );
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Access Restricted');
    expect(alert).toHaveTextContent('Reference ID: req-999-authz');
  });

  it('ErrorState renders retry button when onRetry is passed', async () => {
    const onRetry = vi.fn();
    render(
      <ErrorState
        severity="error"
        title="Gateway Disconnect"
        onRetry={onRetry}
      />
    );
    const retryBtn = screen.getByRole('button', { name: 'Retry Operation' });
    await userEvent.click(retryBtn);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
