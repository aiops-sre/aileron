// Command palette — global search and action launcher.
// Cmd+K to open. Searches resources, investigations, playbooks, navigation.
import { useState, useEffect, useCallback } from 'react';
import { Command } from 'cmdk';
import { motion } from 'framer-motion';
import { useNavigate } from 'react-router-dom';
import {
  Search, LayoutDashboard, Server, Network, Shield,
  Activity, GitBranch, Terminal, Zap, ArrowRight,
} from 'lucide-react';
import { useStore } from '@/store';

const COMMANDS = [
  { id: 'dashboard',    label: 'Go to Dashboard',    icon: LayoutDashboard, action: 'nav', to: '/' },
  { id: 'workloads',   label: 'Go to Workloads',     icon: Server,          action: 'nav', to: '/workloads' },
  { id: 'topology',    label: 'Open Topology Graph',  icon: Network,         action: 'nav', to: '/topology' },
  { id: 'investigate', label: 'New Investigation',    icon: Search,          action: 'nav', to: '/investigate' },
  { id: 'security',    label: 'Security Posture',     icon: Shield,          action: 'nav', to: '/security' },
  { id: 'intelligence',label: 'SRE Intelligence',     icon: Activity,        action: 'nav', to: '/intelligence' },
  { id: 'terminal',    label: 'Toggle Terminal',      icon: Terminal,        action: 'terminal' },
];

interface Props { onClose: () => void; }

export default function CommandPalette({ onClose }: Props) {
  const [query, setQuery] = useState('');
  const navigate = useNavigate();
  const { setTerminalOpen, terminalOpen } = useStore();

  const run = useCallback((cmd: typeof COMMANDS[0]) => {
    if (cmd.action === 'nav' && cmd.to) {
      navigate(cmd.to);
    } else if (cmd.action === 'terminal') {
      setTerminalOpen(!terminalOpen);
    }
    onClose();
  }, [navigate, terminalOpen]);

  const filtered = query
    ? COMMANDS.filter(c => c.label.toLowerCase().includes(query.toLowerCase()))
    : COMMANDS;

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh] px-4"
      onClick={onClose}
    >
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <motion.div
        initial={{ scale: 0.97, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        exit={{ scale: 0.97, opacity: 0 }}
        className="relative w-full max-w-lg bg-surface-raised border border-surface-border rounded-xl shadow-2xl overflow-hidden z-10"
        onClick={e => e.stopPropagation()}
      >
        <Command>
          <div className="flex items-center gap-3 px-4 py-3 border-b border-surface-border">
            <Search size={14} className="text-gray-500 flex-shrink-0" />
            <Command.Input
              value={query}
              onValueChange={setQuery}
              placeholder="Search commands, resources, investigations..."
              className="flex-1 bg-transparent text-sm text-gray-100 placeholder-gray-500 outline-none"
              autoFocus
            />
            <kbd className="kbd text-xs">Esc</kbd>
          </div>

          <Command.List className="max-h-80 overflow-y-auto p-2">
            <Command.Empty className="py-6 text-center text-xs text-gray-500">
              No results for "{query}"
            </Command.Empty>

            {filtered.length > 0 && (
              <Command.Group heading={
                <span className="px-2 text-xs text-gray-600 font-medium uppercase tracking-wider">
                  {query ? 'Results' : 'Commands'}
                </span>
              }>
                {filtered.map(cmd => {
                  const Icon = cmd.icon;
                  return (
                    <Command.Item
                      key={cmd.id}
                      value={cmd.label}
                      onSelect={() => run(cmd)}
                      className="flex items-center gap-3 px-3 py-2 rounded-md text-sm text-gray-300
                                 cursor-pointer hover:bg-surface-overlay hover:text-white
                                 data-[selected=true]:bg-brand-glow data-[selected=true]:text-brand
                                 transition-colors"
                    >
                      <Icon size={14} className="flex-shrink-0 opacity-70" />
                      <span className="flex-1">{cmd.label}</span>
                      <ArrowRight size={12} className="opacity-30" />
                    </Command.Item>
                  );
                })}
              </Command.Group>
            )}
          </Command.List>

          <div className="flex items-center gap-3 px-4 py-2 border-t border-surface-border text-xs text-gray-600">
            <span><kbd className="kbd">↑↓</kbd> navigate</span>
            <span><kbd className="kbd">Enter</kbd> select</span>
            <span><kbd className="kbd">Esc</kbd> close</span>
          </div>
        </Command>
      </motion.div>
    </motion.div>
  );
}
