import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Sidebar } from './Sidebar';
import { CapabilitiesContext } from '../../features/capabilities';
import { AuthContext, type AuthContextValue } from '../../auth/AuthContext';

function renderSidebar(route = 'overview', onRouteChange = vi.fn()) {
  const auth: AuthContextValue = {
    user: { principal_id: 'operator-1', role: 'admin', authorities: ['*'] },
    isAuthenticated: true,
    isLoading: false,
    login: vi.fn(),
    logout: vi.fn(),
    refreshSession: vi.fn(),
  };

  return render(
    <AuthContext.Provider value={auth}>
      <CapabilitiesContext.Provider
        value={{
          capabilities: {},
          hasCapability: () => true,
          getCapabilityState: () => 'AVAILABLE',
          getCapabilityReason: () => undefined,
        }}
      >
        <Sidebar currentRoute={route} onRouteChange={onRouteChange} />
      </CapabilitiesContext.Provider>
    </AuthContext.Provider>
  );
}

describe('Sidebar (T180)', () => {
  it('renders navigation links for available features', () => {
    renderSidebar();
    expect(screen.getByRole('button', { name: /Overview/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Tasks/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Memory/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Audit Log/i })).toBeInTheDocument();
  });

  it('navigates to route on click', async () => {
    const onRouteChange = vi.fn();
    renderSidebar('overview', onRouteChange);

    await userEvent.click(screen.getByRole('button', { name: /Tasks/i }));
    expect(onRouteChange).toHaveBeenCalledWith('tasks');
  });
});
