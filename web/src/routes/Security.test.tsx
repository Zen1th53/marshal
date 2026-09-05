import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Security } from './Security';
import { api } from '../api/client';

const mockSecurityData = {
  policy_id: 'POL-MARSHAL-MAIN-2026',
  revision: 42,
  global_risk_level: 'LOW',
  degraded_controls: [],
  gate_rules: [
    {
      id: 'GATE-001-LOOPBACK',
      name: 'Strict Loopback Bind Constraint',
      enforcement: 'mandatory',
      status: 'enforced',
      description: 'Web Control Plane MUST strictly bind to 127.0.0.1.',
      last_evaluated_at: '2026-08-20T00:00:00Z',
    },
  ],
  capability_rules: [
    {
      capability_name: 'cap:task:merge',
      required_role: 'admin',
      decision: 'ALLOWED',
    },
    {
      capability_name: 'cap:worktree:force_reset',
      required_role: 'system',
      decision: 'DENIED',
      denial_reason: 'Direct destructive worktree mutations are prohibited.',
    },
  ],
  last_audited_at: '2026-08-20T00:00:00Z',
};

describe('Security Inspector Route (T197)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders security policy header, gate rules, and capability decisions', async () => {
    vi.spyOn(api, 'getSecurityPolicy').mockResolvedValueOnce(mockSecurityData);

    render(<Security />);
    expect(await screen.findByText(/Security Policy & Governed Control Plane/i)).toBeInTheDocument();
    expect(screen.getByText('POL-MARSHAL-MAIN-2026')).toBeInTheDocument();
    expect(screen.getByText('Strict Loopback Bind Constraint')).toBeInTheDocument();
    expect(screen.getByText('cap:task:merge')).toBeInTheDocument();
    expect(screen.getByText('ALLOWED')).toBeInTheDocument();
    expect(screen.getByText('DENIED')).toBeInTheDocument();
    expect(screen.getByText('Direct destructive worktree mutations are prohibited.')).toBeInTheDocument();
  });

  it('switches to governed policy editor tab', async () => {
    vi.spyOn(api, 'getSecurityPolicy').mockResolvedValueOnce(mockSecurityData);

    render(<Security />);
    const editorTab = await screen.findByRole('tab', { name: /Governed Policy Editor/i });
    await userEvent.click(editorTab);

    expect(screen.getByText(/Editing Policy Draft for/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Validate Syntax/i })).toBeInTheDocument();
  });
});
