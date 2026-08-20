import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';

interface BenchmarkMetric {
  name: string;
  value: number;
  unit: string;
  baseline: number;
  threshold: number;
}

interface BenchmarkReport {
  suite_id: string;
  suite_name: string;
  harness_type: string;
  status: string;
  dataset_subset: string;
  commit_sha: string;
  metrics: BenchmarkMetric[];
  scope_notice: string;
  evaluated_at: string;
}

export function Benchmarks() {
  const [reports, setReports] = useState<BenchmarkReport[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchBenchmarks = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listBenchmarks();
      setReports(resp.reports);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query benchmark reports');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchBenchmarks();
  }, [fetchBenchmarks]);

  return (
    <div className="benchmarks-container">
      <div className="memory-header">
        <div className="memory-headline">
          <h2 className="memory-title">Benchmarks & Conformance Dashboard</h2>
          <span className="memory-subtitle">
            Rigorous evaluation of long-horizon retrieval recall, adversarial poisoning defense, and consensus latency
          </span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchBenchmarks}>
          Refresh Benchmarks
        </Button>
      </div>

      {/* Honest Boundaries Disclaimer */}
      <div className="alert-box alert-info" style={{ marginBottom: 'var(--space-4)' }}>
        <strong>Benchmark Honesty Invariant:</strong> Synthetic and subset evaluations are labeled <code>INTERNAL-COMPATIBLE</code> and represent bounded empirical observations, not universal mathematical security proofs. Full official harness evaluations require dedicated sandbox infrastructure and are labeled <code>NOT_RUN</code> when unexecuted.
      </div>

      {loading ? (
        <LoadingState message="Loading benchmark artifacts and evaluating threshold regressions…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchBenchmarks} />
      ) : reports.length === 0 ? (
        <EmptyState title="No benchmark reports" description="Run the test harness to generate reports." />
      ) : (
        <div className="benchmarks-grid">
          {reports.map((rep) => (
            <div key={rep.suite_id} className="benchmark-card">
              <div className="benchmark-card-header">
                <div className="flex-row items-center gap-2">
                  <h3 className="benchmark-suite-title">{rep.suite_name}</h3>
                  <span className={`diff-tag ${rep.harness_type === 'internal_compatible' ? 'tag-modified' : 'tag-deleted'}`}>
                    {rep.harness_type === 'internal_compatible' ? 'INTERNAL-COMPATIBLE' : 'OFFICIAL-FULL'}
                  </span>
                </div>
                <StatusBadge
                  status={rep.status === 'PASSED' ? 'ready' : rep.status === 'NOT_RUN' ? 'pending' : 'degraded'}
                  label={rep.status}
                />
              </div>

              <div className="benchmark-meta flex-row gap-4 text-xs text-dim" style={{ margin: 'var(--space-2) 0' }}>
                <span>Dataset: <code>{rep.dataset_subset}</code></span>
                <span>Commit: <code>{rep.commit_sha}</code></span>
              </div>

              {rep.metrics.length > 0 ? (
                <div className="table-responsive" style={{ margin: 'var(--space-3) 0' }}>
                  <table className="data-table" aria-label={`Metrics for ${rep.suite_name}`}>
                    <thead>
                      <tr>
                        <th>Metric</th>
                        <th>Observed</th>
                        <th>Baseline</th>
                        <th>Threshold</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rep.metrics.map((m) => (
                        <tr key={m.name}>
                          <td><strong>{m.name}</strong></td>
                          <td className="font-mono text-xs font-bold text-success">
                            {m.value} {m.unit}
                          </td>
                          <td className="font-mono text-xs text-dim">
                            {m.baseline} {m.unit}
                          </td>
                          <td className="font-mono text-xs text-dim">
                            {m.threshold} {m.unit}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="p-3 bg-surface-alt rounded text-xs text-dim" style={{ margin: 'var(--space-3) 0' }}>
                  Suite unexecuted in current environment. No empirical metrics available.
                </div>
              )}

              <p className="benchmark-scope-notice text-xs text-dim">
                <strong>Scope Notice:</strong> {rep.scope_notice}
              </p>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
