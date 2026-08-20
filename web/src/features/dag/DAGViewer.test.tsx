import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DAGViewer } from './DAGViewer';
import { api } from '../../api/client';

const mockDAGResponse = {
  nodes: [
    {
      id: 'TASK-001',
      title: 'Memory System',
      status: 'completed' as const,
      risk: 'HIGH',
      assigned_to: 'agent-claude',
      layer: 0,
    },
    {
      id: 'TASK-002',
      title: 'Web Control Plane',
      status: 'running' as const,
      risk: 'CRITICAL',
      assigned_to: 'agent-codex',
      layer: 1,
    },
  ],
  edges: [
    {
      source_id: 'TASK-001',
      target_id: 'TASK-002',
      type: 'depends_on',
    },
  ],
  has_cycles: false,
  max_depth: 5,
};

describe('DAGViewer (T184)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders deterministic layers and selects node on click', async () => {
    vi.spyOn(api, 'getTaskDAG').mockResolvedValue(mockDAGResponse);
    const onSelectTask = vi.fn();

    render(<DAGViewer onSelectTask={onSelectTask} />);
    expect(await screen.findByText('Deterministic Task DAG')).toBeInTheDocument();
    expect(screen.getByText('Layer 0')).toBeInTheDocument();
    expect(screen.getByText('Layer 1')).toBeInTheDocument();

    const nodeCard = (await screen.findByText('Web Control Plane')).closest('.dag-node-card')!;
    expect(nodeCard).not.toBeNull();
    await userEvent.click(nodeCard);

    expect(onSelectTask).toHaveBeenCalledWith('TASK-002');
  });

  it('displays cycle warning alert if cycle exists', async () => {
    vi.spyOn(api, 'getTaskDAG').mockResolvedValueOnce({
      ...mockDAGResponse,
      has_cycles: true,
      cycle_path: ['TASK-001', 'TASK-002', 'TASK-001'],
    });

    render(<DAGViewer />);
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.getByText(/TASK-001 → TASK-002 → TASK-001/i)).toBeInTheDocument();
  });
});
