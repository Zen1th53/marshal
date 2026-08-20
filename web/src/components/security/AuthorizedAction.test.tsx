import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AuthorizedAction } from './AuthorizedAction';
import { AuthContext, type AuthContextValue } from '../../auth/AuthContext';

function renderWithAuth(auth: Partial<AuthContextValue>, ui: React.ReactElement) {
  const fullAuth: AuthContextValue = {
    user: null,
    isAuthenticated: false,
    isLoading: false,
    login: vi.fn(),
    logout: vi.fn(),
    refreshSession: vi.fn(),
    ...auth,
  };

  return render(<AuthContext.Provider value={fullAuth}>{ui}</AuthContext.Provider>);
}

describe('AuthorizedAction (T176)', () => {
  it('renders disabled button with aria-label when authority is missing', () => {
    renderWithAuth(
      {
        user: { principal_id: 'viewer-1', role: 'viewer', authorities: [] },
      },
      <AuthorizedAction authority="release.approve" onAction={vi.fn()}>
        Approve Release
      </AuthorizedAction>
    );

    const btn = screen.getByRole('button', { name: /Action disabled: requires release.approve authority/i });
    expect(btn).toBeDisabled();
  });

  it('executes direct action when authorized and non-destructive', async () => {
    const onAction = vi.fn();
    renderWithAuth(
      {
        user: { principal_id: 'admin-1', role: 'admin', authorities: ['*'] },
      },
      <AuthorizedAction authority="task.plan" onAction={onAction}>
        Plan Task
      </AuthorizedAction>
    );

    const btn = screen.getByRole('button', { name: /Plan Task/i });
    expect(btn).not.toBeDisabled();
    await userEvent.click(btn);
    expect(onAction).toHaveBeenCalledTimes(1);
  });

  it('prompts confirmation modal for destructive action and executes on confirm', async () => {
    const onAction = vi.fn();
    renderWithAuth(
      {
        user: { principal_id: 'admin-1', role: 'admin', authorities: ['*'] },
      },
      <AuthorizedAction
        authority="release.approve"
        isDestructive
        confirmTitle="Confirm Gate Override"
        confirmMessage="This will bypass review."
        onAction={onAction}
      >
        Override Gate
      </AuthorizedAction>
    );

    await userEvent.click(screen.getByRole('button', { name: /Override Gate/i }));
    // Modal opens
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('This will bypass review.')).toBeInTheDocument();
    expect(onAction).not.toHaveBeenCalled();

    // Confirm execution
    await userEvent.click(screen.getByRole('button', { name: /Confirm Action/i }));
    expect(onAction).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
