import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Benchmarks } from './Benchmarks';
import { api } from '../api/client';

const mockBenchmarksData = {
  reports: [
    {
      suite_id: 'BM-LOCOMO-01',
      suite_name: 'LoCoMo Long-Horizon Memory Retrieval',
      harness_type: 'internal_compatible',
      status: 'PASSED',
      dataset_subset: 'locomo_eval_250_turns',
      commit_sha: 'de45aa2',
      metrics: [
        { name: 'Recall@10', value: 0.942, unit: 'ratio', baseline: 0.88, threshold: 0.9 },
      ],
      scope_notice: 'Executed on internal synthetic corpus.',
      evaluated_at: '2026-08-20T00:00:00Z',
    },
    {
      suite_id: 'BM-SWEBENCH-FULL-04',
      suite_name: 'SWE-Bench Verified Full Harness',
      harness_type: 'official_full',
      status: 'NOT_RUN',
      dataset_subset: 'swebench_verified_500',
      commit_sha: 'de45aa2',
      metrics: [],
      scope_notice: 'Requires cloud sandbox cluster.',
      evaluated_at: '2026-08-20T00:00:00Z',
    },
  ],
  total_suites: 2,
  evaluated_at: '2026-08-20T00:00:00Z',
};

describe('Benchmarks Route (T212)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders benchmark suites, metrics, and honest labels', async () => {
    vi.spyOn(api, 'listBenchmarks').mockResolvedValueOnce(mockBenchmarksData);

    render(<Benchmarks />);

    expect(await screen.findByText('Benchmarks & Conformance Dashboard')).toBeInTheDocument();
    expect(screen.getByText('LoCoMo Long-Horizon Memory Retrieval')).toBeInTheDocument();
    expect(screen.getAllByText('INTERNAL-COMPATIBLE').length).toBeGreaterThan(0);
    expect(screen.getByText('SWE-Bench Verified Full Harness')).toBeInTheDocument();
    expect(screen.getByText('OFFICIAL-FULL')).toBeInTheDocument();
    expect(screen.getAllByText('NOT_RUN').length).toBeGreaterThan(0);
  });
});
