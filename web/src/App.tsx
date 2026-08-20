import { useState, useEffect, useCallback } from 'react';
import { AuthProvider, useAuth } from './auth/AuthContext';
import { CapabilitiesContext } from './features/capabilities';
import { RealtimeProvider } from './realtime/store';
import { ToastProvider, ToastContainer } from './components/toast';
import { Header } from './components/layout/Header';
import { Sidebar } from './components/nav/Sidebar';
import { CommandPalette } from './components/command/CommandPalette';
import { Login } from './routes/Login';
import { Overview } from './routes/Overview';
import { Agents } from './routes/Agents';
import { Tasks } from './routes/Tasks';
import { Runs } from './routes/Runs';
import { Review } from './routes/Review';
import { Evidence } from './routes/Evidence';
import { Trace } from './routes/Trace';
import { Providers } from './routes/Providers';
import { Security } from './routes/Security';
import { Audit } from './routes/Audit';
import { Memory } from './routes/Memory';
import { api } from './api/client';
import type { CapabilityStatusDTO } from './api/types';

function MainApp() {
  const { isAuthenticated, isLoading } = useAuth();
  const [currentRoute, setCurrentRoute] = useState('overview');
  const [isCommandPaletteOpen, setIsCommandPaletteOpen] = useState(false);
  const [capabilities, setCapabilities] = useState<Record<string, CapabilityStatusDTO>>({});

  const loadCapabilities = useCallback(async () => {
    try {
      const resp = await api.getCapabilities();
      if (resp && resp.capabilities) {
        setCapabilities(resp.capabilities as Record<string, CapabilityStatusDTO>);
      }
    } catch {
      // Fallback
    }
  }, []);

  useEffect(() => {
    if (isAuthenticated) {
      void loadCapabilities();
    }
  }, [isAuthenticated, loadCapabilities]);

  // Global keyboard shortcut for Command Palette (Cmd+K / Ctrl+K)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setIsCommandPaletteOpen((prev) => !prev);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  if (isLoading) {
    return (
      <div className="state-view state-loading" style={{ minHeight: '100vh', margin: 0 }}>
        <div className="spinner" aria-hidden="true" />
        <p className="state-message">Initializing MARSHAL Control Plane…</p>
      </div>
    );
  }

  if (!isAuthenticated) {
    return <Login />;
  }

  return (
    <CapabilitiesContext.Provider
      value={{
        capabilities,
        hasCapability: (name) => capabilities[name]?.state === 'AVAILABLE',
        getCapabilityState: (name) => capabilities[name]?.state ?? 'UNAVAILABLE',
        getCapabilityReason: (name) => capabilities[name]?.reason,
      }}
    >
      <div className="app-shell">
        <Header onOpenCommandPalette={() => setIsCommandPaletteOpen(true)} />
        <div className="app-body">
          <Sidebar currentRoute={currentRoute} onRouteChange={setCurrentRoute} />
          <main className="app-main" id="main-content" role="main">
            {currentRoute === 'overview' && (
              <Overview onNavigate={(route) => setCurrentRoute(route as any)} />
            )}
            {currentRoute === 'agents' && <Agents />}
            {currentRoute === 'tasks' && <Tasks onNavigateRuns={(_taskId) => setCurrentRoute('runs')} />}
            {currentRoute === 'runs' && <Runs />}
            {currentRoute === 'review' && <Review />}
            {currentRoute === 'evidence' && <Evidence />}
            {currentRoute === 'trace' && <Trace />}
            {currentRoute === 'providers' && <Providers />}
            {currentRoute === 'security' && <Security />}
            {currentRoute === 'audit' && <Audit />}
            {currentRoute === 'memory' && <Memory />}
            {currentRoute !== 'overview' && currentRoute !== 'agents' && currentRoute !== 'tasks' && currentRoute !== 'runs' && currentRoute !== 'review' && currentRoute !== 'evidence' && currentRoute !== 'trace' && currentRoute !== 'providers' && currentRoute !== 'security' && currentRoute !== 'audit' && currentRoute !== 'memory' && (
              <div className="placeholder-view">
                <h2>{currentRoute.toUpperCase()} Control Surface</h2>
                <p>Telemetry, controls and state introspection for {currentRoute}.</p>
              </div>
            )}
          </main>
        </div>

        <CommandPalette
          isOpen={isCommandPaletteOpen}
          onClose={() => setIsCommandPaletteOpen(false)}
          onNavigate={setCurrentRoute}
        />
        <ToastContainer />
      </div>
    </CapabilitiesContext.Provider>
  );
}

export default function App() {
  return (
    <AuthProvider>
      <RealtimeProvider>
        <ToastProvider>
          <MainApp />
        </ToastProvider>
      </RealtimeProvider>
    </AuthProvider>
  );
}
