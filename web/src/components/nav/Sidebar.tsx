import { useCapabilities } from '../../features/capabilities';
import { useAuth } from '../../auth/AuthContext';

export interface NavItem {
  id: string;
  label: string;
  icon: string;
  path: string;
  requiredCapability?: string;
}

export const NAV_ITEMS: NavItem[] = [
  { id: 'overview',   label: 'Overview',   icon: '⊞', path: '/' },
  { id: 'agents',     label: 'Agents',     icon: '👥', path: '/agents',     requiredCapability: 'cap:agent:read' },
  { id: 'tasks',      label: 'Tasks',      icon: '📋', path: '/tasks',      requiredCapability: 'cap:task:read' },
  { id: 'runs',       label: 'Runs',       icon: '▶',  path: '/runs',       requiredCapability: 'cap:task:read' },
  { id: 'review',     label: 'Review',     icon: '🛡',  path: '/review',     requiredCapability: 'cap:gate:read' },
  { id: 'memory',     label: 'Memory',     icon: '🧠', path: '/memory',     requiredCapability: 'cap:memory:read' },
  { id: 'evidence',   label: 'Evidence',   icon: '📜', path: '/evidence',   requiredCapability: 'cap:audit:read' },
  { id: 'providers',  label: 'Providers',  icon: '🔌', path: '/providers',  requiredCapability: 'cap:adapter:read' },
  { id: 'security',   label: 'Security',   icon: '🔒', path: '/security',   requiredCapability: 'cap:system:read' },
  { id: 'audit',      label: 'Audit Log',  icon: '📑', path: '/audit',      requiredCapability: 'cap:audit:read' },
  { id: 'benchmarks', label: 'Benchmarks', icon: '📊', path: '/benchmarks', requiredCapability: 'cap:system:read' },
  { id: 'operations', label: 'Operations', icon: '⚙',  path: '/operations', requiredCapability: 'cap:system:read' },
  { id: 'settings',   label: 'Settings',   icon: '🛠',  path: '/settings' },
];

interface SidebarProps {
  currentRoute: string;
  onRouteChange: (routeId: string) => void;
}

export function Sidebar({ currentRoute, onRouteChange }: SidebarProps) {
  const { hasCapability } = useCapabilities();
  const { user } = useAuth();

  return (
    <aside className="app-sidebar" role="navigation" aria-label="Sidebar navigation">
      <nav className="sidebar-nav">
        {NAV_ITEMS.map((item) => {
          // If item requires a capability, check availability
          if (item.requiredCapability && !hasCapability(item.requiredCapability)) {
            return null;
          }

          const isActive = currentRoute === item.id;
          return (
            <button
              key={item.id}
              type="button"
              className={`sidebar-link ${isActive ? 'active' : ''}`}
              onClick={() => onRouteChange(item.id)}
              aria-current={isActive ? 'page' : undefined}
            >
              <span className="sidebar-icon" aria-hidden="true">{item.icon}</span>
              <span className="sidebar-label">{item.label}</span>
            </button>
          );
        })}
      </nav>

      {user && (
        <div className="sidebar-footer">
          <div className="user-badge" title={`Role: ${user.role}`}>
            <span className="user-icon">👤</span>
            <div className="user-info">
              <span className="user-id">{user.principal_id}</span>
              <span className="user-role">{user.role}</span>
            </div>
          </div>
        </div>
      )}
    </aside>
  );
}
