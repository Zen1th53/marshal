import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Providers } from './Providers';
import { ToastProvider } from '../components/toast';
import { api } from '../api/client';

const mockProvidersData = {
  providers: [
    {
      id: 'anthropic',
      name: 'Anthropic Claude',
      class: 'cloud',
      probe_status: 'healthy',
      capabilities: ['reasoning', 'tool_use'],
      models: [
        {
          id: 'claude-3-7-sonnet',
          context_window: 200000,
          latency_p95_ms: 850,
        },
      ],
      last_probed_at: '2026-08-20T00:00:00Z',
    },
  ],
  routing_decisions: [
    {
      intent: 'planning',
      selected_model: 'claude-3-7-sonnet',
      provider_id: 'anthropic',
      rationale: 'Evaluated as optimal for architectural planning.',
      is_pinned: false,
    },
  ],
  last_evaluated_at: '2026-08-20T00:00:00Z',
};

describe('Providers Route (T196)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders provider fleet cards and router decision table', async () => {
    vi.spyOn(api, 'getProviders').mockResolvedValueOnce(mockProvidersData);

    render(
      <ToastProvider>
        <Providers />
      </ToastProvider>
    );

    expect(await screen.findByText('Model Provider Fleet & Router Matrix')).toBeInTheDocument();
    expect(screen.getByText('Anthropic Claude')).toBeInTheDocument();
    expect(screen.getByText('HEALTHY')).toBeInTheDocument();
    expect(screen.getByText('Evaluated as optimal for architectural planning.')).toBeInTheDocument();
  });

  it('toggles model pin on override button click', async () => {
    vi.spyOn(api, 'getProviders').mockResolvedValue(mockProvidersData);
    const overrideSpy = vi.spyOn(api, 'overrideRouter').mockResolvedValueOnce({
      intent: 'planning',
      model_id: 'claude-3-7-sonnet',
      is_pinned: true,
      status: 'updated',
    });

    render(
      <ToastProvider>
        <Providers />
      </ToastProvider>
    );

    const pinBtn = await screen.findByRole('button', { name: /Pin Model/i });
    await userEvent.click(pinBtn);

    expect(overrideSpy).toHaveBeenCalledWith({
      intent: 'planning',
      model_id: 'claude-3-7-sonnet',
      is_pinned: true,
    });
  });
});
