import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RetrievalExplainability } from './RetrievalExplainability';
import { api } from '../../../api/client';

const mockExplainData = {
  query: 'loopback invariant',
  embedder_model: 'text-embedding-3-large',
  embedder_status: 'ready',
  fusion_algorithm: 'RRF-k60',
  candidates: [
    {
      memory_id: 'MEM-001-ARCH-DECISION',
      title: 'Loopback Architecture Invariant',
      kind: 'decision',
      scope: 'project',
      lexical_rank: 1,
      lexical_score: 0.94,
      dense_rank: 1,
      dense_score: 0.96,
      graph_bonus: 0.05,
      freshness_penalty: 0.0,
      final_rrf_score: 0.95,
      rerank_rationale: 'Rank 1 in both BM25 and dense vector search.',
    },
  ],
  evaluated_at: '2026-08-20T00:00:00Z',
};

describe('RetrievalExplainability Component (T202)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders RRF candidate breakdown and embedder status', async () => {
    vi.spyOn(api, 'explainRetrieval').mockResolvedValueOnce(mockExplainData);

    render(<RetrievalExplainability query="loopback invariant" onClose={vi.fn()} />);
    expect(await screen.findByText('Hybrid Retrieval & RRF Fusion Explainability')).toBeInTheDocument();
    expect(screen.getByText('text-embedding-3-large')).toBeInTheDocument();
    expect(screen.getByText('Loopback Architecture Invariant')).toBeInTheDocument();
    expect(screen.getByText('Rank 1 in both BM25 and dense vector search.')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', async () => {
    vi.spyOn(api, 'explainRetrieval').mockResolvedValueOnce(mockExplainData);
    const closeFn = vi.fn();

    render(<RetrievalExplainability query="loopback invariant" onClose={closeFn} />);
    const closeBtn = await screen.findByLabelText('Close');
    await userEvent.click(closeBtn);

    expect(closeFn).toHaveBeenCalled();
  });
});
