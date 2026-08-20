import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CreateTaskModal } from './CreateTaskModal';
import { ToastProvider } from '../../../components/toast';
import { api } from '../../../api/client';

describe('CreateTaskModal (T186)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('validates required title and calls api.createTask with idempotency key', async () => {
    const createTaskSpy = vi.spyOn(api, 'createTask').mockResolvedValueOnce({
      id: 'TASK-999',
      title: 'Automated Security Probe',
      status: 'ready' as const,
      risk: 'HIGH',
      base_commit: '4431cce',
      head_commit: '4431cce',
      created_at: '2026-08-20T00:00:00Z',
      updated_at: '2026-08-20T00:00:00Z',
    });

    const onClose = vi.fn();
    const onSuccess = vi.fn();

    render(
      <ToastProvider>
        <CreateTaskModal isOpen={true} onClose={onClose} onSuccess={onSuccess} />
      </ToastProvider>
    );

    const titleInput = screen.getByLabelText(/Task Title/i);
    await userEvent.type(titleInput, 'Automated Security Probe');

    const descInput = screen.getByLabelText(/Objective & Plan/i);
    await userEvent.type(descInput, 'Run security fuzzing');

    const submitBtn = screen.getByRole('button', { name: /Create Task/i });
    await userEvent.click(submitBtn);

    expect(createTaskSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Automated Security Probe',
        description: 'Run security fuzzing',
      }),
      expect.stringMatching(/^task-create-/)
    );
    expect(onSuccess).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });
});
