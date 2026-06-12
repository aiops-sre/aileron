import { BrowserRouter, Routes, Route, NavLink } from 'react-router-dom';
import { useEffect, useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { motion, AnimatePresence } from 'framer-motion';
import {
  LayoutDashboard, Server, Network, Shield, DollarSign,
  Search, Terminal, GitBranch, Zap, AlertTriangle,
  ChevronDown, Settings, RefreshCw, Activity,
} from 'lucide-react';
import { api } from '@/api/kubesense';
import { useStore } from '@/store';
import clsx from 'clsx';

// Views
import Dashboard    from '@/views/Dashboard';
import Workloads    from '@/views/Workloads';
import Topology     from '@/views/Topology';
import Investigate  from '@/views/Investigate';
import Security     from '@/views/Security';
import Intelligence from '@/views/Intelligence';
import TerminalView from '@/views/Terminal';
import CommandPalette from '@/components/CommandPalette';

const NAV = [
  { to: '/',            icon: LayoutDashboard, label: 'Dashboard' },
  { to: '/workloads',   icon: Server,          label: 'Workloads' },
  { to: '/topology',    icon: Network,         label: 'Topology' },
  { to: '/investigate', icon: Search,          label: 'Investigate' },
  { to: '/security',    icon: Shield,          label: 'Security' },
  { to: '/intelligence',icon: Activity,        label: 'Intelligence' },
];

export default function App() {
  const {
    activeClusterId, clusters, activeNamespace,
    setClusters, setActiveCluster, setNamespace,
    commandPaletteOpen, setCommandPaletteOpen,
    terminalOpen, setTerminalOpen,
  } = useStore();

  const { data, isLoading } = useQuery({
    queryKey: ['clusters'],
    queryFn: api.listClusters,
    refetchInterval: 30_000,
  });

  useEffect(() => {
    if (data?.clusters) {
      setClusters(data.clusters);
      if (!activeClusterId && data.clusters.length > 0) {
        setActiveCluster(data.clusters[0].id);
      }
    }
  }, [data]);

  // Global keyboard shortcuts
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setCommandPaletteOpen(true);
      }
      if ((e.metaKey || e.ctrlKey) && e.key === '`') {
        e.preventDefault();
        setTerminalOpen(!terminalOpen);
      }
      if (e.key === 'Escape') {
        setCommandPaletteOpen(false);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [terminalOpen]);

  const activeCluster = clusters.find(c => c.id === activeClusterId);

  return (
    <BrowserRouter>
      <div className="flex h-screen overflow-hidden bg-surface">

        {/* ── Left sidebar ──────────────────────────────────────────── */}
        <aside className="w-48 flex-shrink-0 flex flex-col border-r border-surface-border bg-surface">
          {/* Logo */}
          <div className="flex items-center gap-2 px-4 py-3 border-b border-surface-border">
            <div className="w-6 h-6 rounded bg-brand flex items-center justify-center">
              <Zap size={14} className="text-white" />
            </div>
            <span className="font-semibold text-white text-sm tracking-wide">KubeSense</span>
          </div>

          {/* Cluster selector */}
          <div className="px-3 py-2 border-b border-surface-border">
            <button
              className="w-full flex items-center justify-between px-2 py-1.5 rounded-md
                         bg-surface-overlay border border-surface-border text-xs text-gray-300
                         hover:border-brand transition-colors"
              onClick={() => {/* open cluster picker */}}
            >
              <span className="truncate max-w-[90px]">
                {activeCluster?.id ?? (isLoading ? 'Loading...' : 'No cluster')}
              </span>
              <ChevronDown size={10} className="text-gray-500 flex-shrink-0" />
            </button>

            {/* Namespace filter */}
            <select
              value={activeNamespace}
              onChange={e => setNamespace(e.target.value)}
              className="mt-1.5 w-full bg-surface text-xs text-gray-400
                         border border-surface-border rounded px-2 py-1
                         focus:outline-none focus:border-brand"
            >
              <option value="all">All namespaces</option>
              <option value="default">default</option>
              <option value="kube-system">kube-system</option>
              <option value="production">production</option>
              <option value="staging">staging</option>
            </select>
          </div>

          {/* Nav */}
          <nav className="flex-1 px-2 py-3 space-y-0.5 overflow-y-auto">
            {NAV.map(({ to, icon: Icon, label }) => (
              <NavLink
                key={to}
                to={to}
                end={to === '/'}
                className={({ isActive }) =>
                  clsx(
                    'flex items-center gap-2.5 px-2.5 py-1.5 rounded-md text-sm transition-colors',
                    isActive
                      ? 'bg-brand-glow text-brand font-medium'
                      : 'text-gray-400 hover:text-gray-100 hover:bg-surface-raised'
                  )
                }
              >
                <Icon size={15} />
                {label}
              </NavLink>
            ))}
          </nav>

          {/* Bottom actions */}
          <div className="px-2 py-2 border-t border-surface-border space-y-0.5">
            <button
              onClick={() => setTerminalOpen(!terminalOpen)}
              className={clsx(
                'w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-md text-sm transition-colors',
                terminalOpen ? 'bg-brand-glow text-brand' : 'text-gray-400 hover:text-gray-100 hover:bg-surface-raised'
              )}
            >
              <Terminal size={15} />
              Terminal
              <kbd className="ml-auto text-xs opacity-50">^`</kbd>
            </button>
            <NavLink
              to="/settings"
              className="flex items-center gap-2.5 px-2.5 py-1.5 rounded-md text-sm
                         text-gray-400 hover:text-gray-100 hover:bg-surface-raised transition-colors"
            >
              <Settings size={15} />
              Settings
            </NavLink>
          </div>
        </aside>

        {/* ── Main area ─────────────────────────────────────────────── */}
        <div className="flex-1 flex flex-col overflow-hidden">

          {/* Top bar */}
          <header className="flex items-center gap-3 px-4 py-2 border-b border-surface-border bg-surface flex-shrink-0">
            {/* Cluster status indicator */}
            {activeCluster && (
              <div className="flex items-center gap-1.5">
                <span className={clsx(
                  'w-1.5 h-1.5 rounded-full',
                  activeCluster.status === 'active' ? 'bg-ok' : 'bg-warn'
                )} />
                <span className="text-xs text-gray-500 font-mono">{activeCluster.id}</span>
                {activeCluster.node_count > 0 && (
                  <span className="badge badge-info">{activeCluster.node_count} nodes</span>
                )}
              </div>
            )}

            {/* Global search / command palette trigger */}
            <button
              onClick={() => setCommandPaletteOpen(true)}
              className="flex-1 max-w-xs flex items-center gap-2 px-3 py-1.5 rounded-md
                         bg-surface-overlay border border-surface-border text-sm text-gray-500
                         hover:border-brand transition-colors cursor-text"
            >
              <Search size={13} />
              <span>Search resources, investigations...</span>
              <kbd className="ml-auto hidden md:flex text-xs opacity-50">Cmd K</kbd>
            </button>

            <div className="flex items-center gap-2 ml-auto">
              <button
                className="btn-ghost p-1.5"
                onClick={() => window.location.reload()}
                title="Refresh"
              >
                <RefreshCw size={14} />
              </button>
              {activeCluster?.last_heartbeat && (
                <span className="text-xs text-gray-600 font-mono hidden lg:block">
                  Last heartbeat {new Date(activeCluster.last_heartbeat).toLocaleTimeString()}
                </span>
              )}
            </div>
          </header>

          {/* Page content */}
          <main className={clsx(
            'flex-1 overflow-hidden transition-all',
            terminalOpen ? 'h-[calc(100%-180px)]' : 'h-full'
          )}>
            <AnimatePresence mode="wait">
              <Routes>
                <Route path="/"            element={<Dashboard />} />
                <Route path="/workloads/*" element={<Workloads />} />
                <Route path="/topology"    element={<Topology />} />
                <Route path="/investigate" element={<Investigate />} />
                <Route path="/security"    element={<Security />} />
                <Route path="/intelligence" element={<Intelligence />} />
                <Route path="/settings"    element={<SettingsView />} />
              </Routes>
            </AnimatePresence>
          </main>

          {/* Integrated terminal panel */}
          <AnimatePresence>
            {terminalOpen && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: 180, opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ duration: 0.15 }}
                className="flex-shrink-0 border-t border-surface-border bg-surface overflow-hidden"
              >
                <TerminalView />
              </motion.div>
            )}
          </AnimatePresence>
        </div>

        {/* Command palette overlay */}
        <AnimatePresence>
          {commandPaletteOpen && <CommandPalette onClose={() => setCommandPaletteOpen(false)} />}
        </AnimatePresence>
      </div>
    </BrowserRouter>
  );
}

function SettingsView() {
  const { apiUrl, apiToken, setApiConfig } = useStore();
  return (
    <div className="p-6 max-w-lg animate-fade-in">
      <h1 className="text-lg font-semibold mb-4">Settings</h1>
      <div className="card p-4 space-y-4">
        <div>
          <label className="block text-xs text-gray-400 mb-1">KubeSense API URL</label>
          <input
            className="input w-full"
            defaultValue={apiUrl}
            placeholder="http://kubesense-api:8080"
            onBlur={e => setApiConfig(e.target.value, apiToken)}
          />
        </div>
        <div>
          <label className="block text-xs text-gray-400 mb-1">API Token</label>
          <input
            className="input w-full"
            type="password"
            defaultValue={apiToken}
            placeholder="Bearer token (leave empty to disable auth)"
            onBlur={e => setApiConfig(apiUrl, e.target.value)}
          />
        </div>
      </div>
    </div>
  );
}
