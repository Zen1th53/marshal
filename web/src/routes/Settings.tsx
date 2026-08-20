import { useState, useEffect, useCallback, type FormEvent } from 'react';
import { api } from '../api/client';
import { Button } from '../components/ui';
import { useToast } from '../components/toast';
import { LoadingState, ErrorState } from '../components/state';

interface SafeEnvDiagnostics {
  os_arch: string;
  go_version: string;
  sandbox_kind: string;
  storage_engine: string;
}

interface SystemSettings {
  revision: number;
  system_mode: string;
  max_concurrent_workers: number;
  telemetry_level: string;
  auto_consolidation_enabled: boolean;
  memory_retention_days: number;
  requires_restart: boolean;
  env_diagnostics: SafeEnvDiagnostics;
  updated_at: string;
}

export function Settings() {
  const [settings, setSettings] = useState<SystemSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Form fields
  const [systemMode, setSystemMode] = useState('strict');
  const [maxWorkers, setMaxWorkers] = useState(4);
  const [telemetryLevel, setTelemetryLevel] = useState('standard');
  const [autoConsolidation, setAutoConsolidation] = useState(true);
  const [retentionDays, setRetentionDays] = useState(30);
  const [saving, setSaving] = useState(false);

  const { addToast } = useToast();

  const fetchSettings = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getSettings();
      setSettings(resp);
      setSystemMode(resp.system_mode);
      setMaxWorkers(resp.max_concurrent_workers);
      setTelemetryLevel(resp.telemetry_level);
      setAutoConsolidation(resp.auto_consolidation_enabled);
      setRetentionDays(resp.memory_retention_days);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query system settings');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchSettings();
  }, [fetchSettings]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!settings) return;

    setSaving(true);
    try {
      const updated = await api.updateSettings({
        expected_revision: settings.revision,
        system_mode: systemMode,
        max_concurrent_workers: maxWorkers,
        telemetry_level: telemetryLevel,
        auto_consolidation_enabled: autoConsolidation,
        memory_retention_days: retentionDays,
      });
      setSettings(updated);
      addToast({
        type: 'success',
        message: `Settings updated to revision #${updated.revision}${updated.requires_restart ? ' (Restart Required)' : ''}`,
      });
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to save settings',
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="settings-container">
      <div className="memory-header">
        <div className="memory-headline">
          <h2 className="memory-title">System Settings & Environment Diagnostics</h2>
          <span className="memory-subtitle">
            Capability-aware, revision-bound system configuration with zero environment variable exposure
          </span>
        </div>
      </div>

      {loading ? (
        <LoadingState message="Loading canonical settings and safe host diagnostics…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchSettings} />
      ) : settings ? (
        <div className="settings-content">
          {settings.requires_restart && (
            <div className="alert-box alert-warning" style={{ marginBottom: 'var(--space-4)' }}>
              <strong>Restart Required:</strong> Concurrency or security mode modifications have been staged and will take full effect upon next daemon reboot.
            </div>
          )}

          <div className="settings-grid">
            {/* Configuration Form */}
            <div className="settings-card">
              <div className="flex-row items-center justify-between" style={{ marginBottom: 'var(--space-3)' }}>
                <h3 className="text-base font-semibold">Engine Configuration</h3>
                <span className="font-mono text-xs text-dim">Revision #{settings.revision}</span>
              </div>

              <form onSubmit={handleSubmit}>
                <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                  <label className="form-label text-xs">System Isolation Mode</label>
                  <select
                    className="form-input form-select text-xs"
                    value={systemMode}
                    onChange={(e) => setSystemMode(e.target.value)}
                  >
                    <option value="standard">Standard (Relaxed boundaries)</option>
                    <option value="strict">Strict (Quorum & sandbox enforced)</option>
                    <option value="airgap">Airgap (Zero outbound network socket)</option>
                  </select>
                </div>

                <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                  <label className="form-label text-xs">Max Concurrent Workers (1–16)</label>
                  <input
                    type="number"
                    min="1"
                    max="16"
                    className="form-input text-xs"
                    value={maxWorkers}
                    onChange={(e) => setMaxWorkers(parseInt(e.target.value, 10))}
                  />
                </div>

                <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                  <label className="form-label text-xs">Telemetry Log Level</label>
                  <select
                    className="form-input form-select text-xs"
                    value={telemetryLevel}
                    onChange={(e) => setTelemetryLevel(e.target.value)}
                  >
                    <option value="minimal">Minimal</option>
                    <option value="standard">Standard</option>
                    <option value="verbose">Verbose</option>
                  </select>
                </div>

                <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                  <label className="form-label text-xs">Memory Retention Days (1–365)</label>
                  <input
                    type="number"
                    min="1"
                    max="365"
                    className="form-input text-xs"
                    value={retentionDays}
                    onChange={(e) => setRetentionDays(parseInt(e.target.value, 10))}
                  />
                </div>

                <div className="form-group flex-row items-center gap-2" style={{ marginBottom: 'var(--space-4)' }}>
                  <label className="flex-row items-center gap-2 text-xs cursor-pointer">
                    <input
                      type="checkbox"
                      checked={autoConsolidation}
                      onChange={(e) => setAutoConsolidation(e.target.checked)}
                    />
                    <span>Enable Automatic Working Memory Consolidation</span>
                  </label>
                </div>

                <Button type="submit" variant="primary" size="sm" disabled={saving}>
                  {saving ? 'Saving Revision…' : 'Save System Settings'}
                </Button>
              </form>
            </div>

            {/* Read-Only Host Environment Diagnostics */}
            <div className="settings-card">
              <h3 className="text-base font-semibold" style={{ marginBottom: 'var(--space-3)' }}>
                Host Environment Diagnostics
              </h3>
              <p className="text-xs text-dim" style={{ marginBottom: 'var(--space-4)' }}>
                Read-only host capabilities. Zero secrets or raw environment variables are exposed over web control surfaces.
              </p>

              <div className="task-meta-grid">
                <div className="meta-box">
                  <span className="meta-label">Architecture</span>
                  <span className="meta-value font-mono text-xs">{settings.env_diagnostics.os_arch}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Go Runtime</span>
                  <span className="meta-value font-mono text-xs">{settings.env_diagnostics.go_version}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Sandbox Engine</span>
                  <span className="meta-value font-mono text-xs">{settings.env_diagnostics.sandbox_kind}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Storage Backend</span>
                  <span className="meta-value font-mono text-xs">{settings.env_diagnostics.storage_engine}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
