import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Login } from './Login';
import { AuthContext, type AuthContextValue } from '../auth/AuthContext';

function renderWithAuth(authOverrides: Partial<AuthContextValue> = {}, onSuccess?: () => void) {
  const defaultAuth: AuthContextValue = {
    user: null,
    isAuthenticated: false,
    isLoading: false,
    login: vi.fn(),
    logout: vi.fn(),
    refreshSession: vi.fn(),
    ...authOverrides,
  };

  return {
    ...render(
      <AuthContext.Provider value={defaultAuth}>
        <Login onSuccess={onSuccess} />
      </AuthContext.Provider>
    ),
    auth: defaultAuth,
  };
}

describe('Login Route (T173)', () => {
  it('renders login form and submits one-time code', async () => {
    const loginMock = vi.fn().mockResolvedValue(undefined);
    const onSuccess = vi.fn();
    renderWithAuth({ login: loginMock }, onSuccess);

    const input = screen.getByLabelText(/One-Time Login Code/i);
    const submitBtn = screen.getByRole('button', { name: /Authenticate Operator/i });

    await userEvent.type(input, 'test-otc-code-123');
    await userEvent.click(submitBtn);

    expect(loginMock).toHaveBeenCalledWith('test-otc-code-123');
  });

  it('displays error message when login fails', async () => {
    const loginMock = vi.fn().mockRejectedValue(new Error('Invalid or expired code'));
    renderWithAuth({ login: loginMock });

    const input = screen.getByLabelText(/One-Time Login Code/i);
    const submitBtn = screen.getByRole('button', { name: /Authenticate Operator/i });

    await userEvent.type(input, 'bad-code');
    await userEvent.click(submitBtn);

    expect(await screen.findByRole('alert')).toHaveTextContent(/Login failed/i);
  });
});
