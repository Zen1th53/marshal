import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { CapabilitiesContext } from './capabilities';
import { RequireCapability } from './Capability';
import type { CapabilityStatusDTO } from '../api/types';

describe('RequireCapability (T171)', () => {
  it('renders children when capability is AVAILABLE', () => {
    const mockCaps: Record<string, CapabilityStatusDTO> = {
      'cap:task:run': { state: 'AVAILABLE', last_checked: '2026-08-20T00:00:00Z' },
    };

    render(
      <CapabilitiesContext.Provider
        value={{
          capabilities: mockCaps,
          hasCapability: (name) => mockCaps[name]?.state === 'AVAILABLE',
          getCapabilityState: (name) => mockCaps[name]?.state ?? 'UNAVAILABLE',
          getCapabilityReason: (name) => mockCaps[name]?.reason,
        }}
      >
        <RequireCapability name="cap:task:run">
          <button>Run Task</button>
        </RequireCapability>
      </CapabilitiesContext.Provider>
    );

    expect(screen.getByRole('button')).toHaveTextContent('Run Task');
  });

  it('renders reason and degraded status when capability is DEGRADED or UNAVAILABLE', () => {
    const mockCaps: Record<string, CapabilityStatusDTO> = {
      'cap:adapter:claude': {
        state: 'UNAVAILABLE',
        reason: 'Anthropic provider API key is not configured in local environment',
        last_checked: '2026-08-20T00:00:00Z',
      },
    };

    render(
      <CapabilitiesContext.Provider
        value={{
          capabilities: mockCaps,
          hasCapability: (name) => mockCaps[name]?.state === 'AVAILABLE',
          getCapabilityState: (name) => mockCaps[name]?.state ?? 'UNAVAILABLE',
          getCapabilityReason: (name) => mockCaps[name]?.reason,
        }}
      >
        <RequireCapability name="cap:adapter:claude">
          <button>Run Claude Task</button>
        </RequireCapability>
      </CapabilitiesContext.Provider>
    );

    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByRole('alert')).toHaveTextContent('Anthropic provider API key is not configured');
  });

  it('renders custom fallback when provided and capability is not available', () => {
    render(
      <CapabilitiesContext.Provider
        value={{
          capabilities: {},
          hasCapability: () => false,
          getCapabilityState: () => 'UNAVAILABLE',
          getCapabilityReason: () => undefined,
        }}
      >
        <RequireCapability name="cap:audit:export" fallback={<div>Export Not Permitted</div>}>
          <button>Export Audit</button>
        </RequireCapability>
      </CapabilitiesContext.Provider>
    );

    expect(screen.getByText('Export Not Permitted')).toBeInTheDocument();
  });
});
