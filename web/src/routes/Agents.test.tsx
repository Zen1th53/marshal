import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Agents } from './Agents';
import { api } from '../api/client';

const mockAgentList = {
  items: [
    {
      id: 'agent-claude-planner',
      name: 'Claude High-Reasoning Planner',
      role: 'planner',
      provider: 'claude',
      model: 'claude-3-7-sonnet',
      status: 'READY',
      capabilities: ['code_edit', 'dag_plan'],
      completed_task_count: 18,
      created_at: '2026-08-20T00:00:00Z',
    },
    {
      id: 'agent-codex-implementer',
      name: 'Codex Rapid Implementer',
      role: 'implementer',
      provider: 'codex',
      model: 'gpt-4o',
      status: 'READY',
      capabilities: ['code_edit', 'git_commit'],
      completed_task_count: 24,
      created_at: '2026-08-20T00:00:00Z',
    },
  ],
  total: 2,
  limit: 50,
};

const mockAgentDetail = {
  id: 'agent-claude-planner',
  name: 'Claude High-Reasoning Planner',
  provider: 'claude',
  model: 'claude-3-7-sonnet',
  status: 'READY',
  capabilities: ['code_edit', 'dag_plan'],
  completed_task_count: 18,
  failed_task_count: 0,
  last_heartbeat: '2026-08-20T00:00:00Z',
  created_at: '2026-08-20T00:00:00Z',
  memory_contributions: {
    episodes_extracted: 14,
    decisions_logged: 32,
    facts_asserted: 45,
  },
};

describe('Agents Route (T182)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders list of registered agents and handles provider filtering', async () => {
    vi.spyOn(api, 'getAgents').mockResolvedValueOnce(mockAgentList);

    render(<Agents />);
    expect(await screen.findByText('Claude High-Reasoning Planner')).toBeInTheDocument();
    expect(screen.getByText('Codex Rapid Implementer')).toBeInTheDocument();

    // Click provider filter tab for CLAUDE
    const claudeTab = screen.getByRole('tab', { name: /CLAUDE/i });
    await userEvent.click(claudeTab);

    expect(screen.getByText('Claude High-Reasoning Planner')).toBeInTheDocument();
    expect(screen.queryByText('Codex Rapid Implementer')).toBeNull();
  });

  it('opens agent detail modal on card click', async () => {
    vi.spyOn(api, 'getAgents').mockResolvedValueOnce(mockAgentList);
    vi.spyOn(api, 'getAgentDetail').mockResolvedValueOnce(mockAgentDetail);

    render(<Agents />);
    const claudeCard = await screen.findByText('Claude High-Reasoning Planner');
    await userEvent.click(claudeCard);

    expect(await screen.findByText('Assigned Capabilities')).toBeInTheDocument();
    expect(screen.getByText('Canonical Memory Contributions')).toBeInTheDocument();
    expect(screen.getByText('32')).toBeInTheDocument(); // Decisions logged
  });
});
