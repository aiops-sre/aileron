// Terminal view — integrated xterm.js kubectl terminal.
import { useEffect, useRef } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import 'xterm/css/xterm.css';
import { X } from 'lucide-react';
import { useStore } from '@/store';

export default function TerminalView() {
  const termRef = useRef<HTMLDivElement>(null);
  const termInstance = useRef<Terminal | null>(null);
  const fitAddon = useRef<FitAddon | null>(null);
  const { setTerminalOpen, activeClusterId, activeNamespace } = useStore();

  useEffect(() => {
    if (!termRef.current || termInstance.current) return;

    const term = new Terminal({
      theme: {
        background: '#0d1117',
        foreground: '#e6edf3',
        cursor: '#4a9eff',
        selectionBackground: 'rgba(74,158,255,0.3)',
        black: '#161b22', red: '#f85149', green: '#3fb950',
        yellow: '#d29922', blue: '#4a9eff', magenta: '#a855f7',
        cyan: '#00cec9', white: '#e6edf3',
      },
      fontFamily: 'JetBrains Mono, Fira Code, monospace',
      fontSize: 12,
      lineHeight: 1.4,
      cursorBlink: true,
      scrollback: 1000,
    });

    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(termRef.current);
    fit.fit();

    termInstance.current = term;
    fitAddon.current = fit;

    // Welcome banner
    const cluster = activeClusterId ?? 'no-cluster';
    const ns = activeNamespace === 'all' ? 'default' : activeNamespace;
    term.writeln(`\x1b[36m KubeSense Terminal \x1b[0m  cluster: \x1b[33m${cluster}\x1b[0m  ns: \x1b[33m${ns}\x1b[0m`);
    term.writeln(`\x1b[90mType kubectl commands below. Results execute against the active cluster.\x1b[0m`);
    term.writeln('');
    term.write('\x1b[32m$ \x1b[0m');

    // Simple REPL — in production this connects to a websocket backend
    let input = '';
    term.onKey(({ key, domEvent }) => {
      if (domEvent.key === 'Enter') {
        term.writeln('');
        if (input.trim()) {
          executeCommand(term, input.trim(), cluster, ns);
        }
        input = '';
        term.write('\x1b[32m$ \x1b[0m');
      } else if (domEvent.key === 'Backspace') {
        if (input.length > 0) {
          input = input.slice(0, -1);
          term.write('\b \b');
        }
      } else if (!domEvent.ctrlKey && !domEvent.altKey) {
        input += key;
        term.write(key);
      }
    });

    return () => { term.dispose(); termInstance.current = null; };
  }, []);

  useEffect(() => {
    const ro = new ResizeObserver(() => fitAddon.current?.fit());
    if (termRef.current) ro.observe(termRef.current);
    return () => ro.disconnect();
  }, []);

  return (
    <div className="h-full flex flex-col">
      <div className="flex items-center justify-between px-3 py-1 border-b border-surface-border bg-surface-overlay flex-shrink-0">
        <span className="text-xs text-gray-500 font-mono">Terminal</span>
        <button className="btn-ghost p-0.5" onClick={() => setTerminalOpen(false)}>
          <X size={12} />
        </button>
      </div>
      <div ref={termRef} className="flex-1 p-1 overflow-hidden" />
    </div>
  );
}

// Simulated command executor — in production connects to websocket proxy
function executeCommand(term: Terminal, cmd: string, cluster: string, ns: string) {
  if (cmd === 'clear') { term.clear(); return; }

  if (cmd.startsWith('kubectl') || cmd.startsWith('k ')) {
    term.writeln(`\x1b[90m[${cluster}/${ns}] ${cmd}\x1b[0m`);
    term.writeln(`\x1b[33mNote: Connect kubectl terminal to kubesense-api websocket proxy for live execution.\x1b[0m`);
    term.writeln(`\x1b[90mConfigured at: ws://kubesense-api:8080/ws/terminal\x1b[0m`);
    return;
  }
  if (cmd === 'help' || cmd === '?') {
    term.writeln(`\x1b[36mKubeSense Terminal — available commands:\x1b[0m`);
    term.writeln(`  kubectl <command>  — execute kubectl commands`);
    term.writeln(`  ks investigate <ns>/<name>  — start RCA investigation`);
    term.writeln(`  ks risk <kind> <ns>/<name>  — score change risk`);
    term.writeln(`  clear  — clear terminal`);
    return;
  }
  if (cmd.startsWith('ks ')) {
    term.writeln(`\x1b[90m${cmd}\x1b[0m`);
    term.writeln(`\x1b[33mKubeSense CLI: connect via kubesense-api HTTP endpoints\x1b[0m`);
    return;
  }
  term.writeln(`\x1b[31mUnknown command: ${cmd}\x1b[0m  type \x1b[36mhelp\x1b[0m for available commands`);
}
