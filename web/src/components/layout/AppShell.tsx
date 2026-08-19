import type { ReactNode } from 'react';

interface AppShellProps {
  children: ReactNode;
}

export function AppShell({ children }: AppShellProps) {
  return (
    <div className="app-shell">
      <header className="app-header" role="banner">
        <div className="app-brand">
          <span className="app-logo" aria-hidden="true">◇</span>
          <h1 className="app-title">MARSHAL</h1>
        </div>
        <nav className="app-nav" role="navigation" aria-label="Main navigation">
          {/* Navigation populated by later tasks */}
        </nav>
      </header>
      <div className="app-body">
        <main className="app-main" role="main" id="main-content">
          {children}
        </main>
      </div>
    </div>
  );
}
