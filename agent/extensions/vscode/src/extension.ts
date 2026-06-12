// KubeSense VS Code Extension
// Brings KubeSense intelligence directly into the editor:
//   - Risk score on YAML save (before kubectl apply)
//   - Cluster intelligence tree view in sidebar
//   - Inline chaos readiness badges on resource files
//   - One-click RCA investigation from YAML file context menu
import * as vscode from 'vscode';

// ─── Activation ───────────────────────────────────────────────────────────────

export function activate(context: vscode.ExtensionContext) {
  const config = () => vscode.workspace.getConfiguration('kubesense');
  const apiUrl  = () => config().get<string>('apiUrl')  ?? 'http://localhost:8080';
  const apiToken = () => config().get<string>('apiToken') ?? '';
  const clusterId = () => config().get<string>('clusterId') ?? '';

  // ── Tree view providers ────────────────────────────────────────────────────
  const explorerProvider = new KubeSenseExplorerProvider(apiUrl, apiToken, clusterId);
  const chaosProvider    = new ChaosReadinessProvider(apiUrl, apiToken, clusterId);
  const playbookProvider = new PlaybookProvider(apiUrl, apiToken, clusterId);

  vscode.window.registerTreeDataProvider('kubesenseExplorer', explorerProvider);
  vscode.window.registerTreeDataProvider('kubesenseChaos',    chaosProvider);
  vscode.window.registerTreeDataProvider('kubesensePlaybooks',playbookProvider);

  // ── Commands ───────────────────────────────────────────────────────────────
  context.subscriptions.push(
    vscode.commands.registerCommand('kubesense.refresh', () => {
      explorerProvider.refresh();
      chaosProvider.refresh();
      playbookProvider.refresh();
      vscode.window.showInformationMessage('KubeSense: refreshed');
    }),

    vscode.commands.registerCommand('kubesense.scoreRisk', async () => {
      const editor = vscode.window.activeTextEditor;
      if (!editor) return;
      const manifest = parseK8sManifest(editor.document.getText());
      if (!manifest) {
        vscode.window.showWarningMessage('KubeSense: Could not parse Kubernetes manifest from this file.');
        return;
      }
      await scoreAndShowRisk(manifest, apiUrl(), apiToken(), clusterId());
    }),

    vscode.commands.registerCommand('kubesense.investigate', async () => {
      const editor = vscode.window.activeTextEditor;
      if (!editor) return;
      const manifest = parseK8sManifest(editor.document.getText());
      if (!manifest) return;
      await triggerInvestigation(manifest, apiUrl(), apiToken(), clusterId());
    }),

    vscode.commands.registerCommand('kubesense.openDashboard', () => {
      const panel = vscode.window.createWebviewPanel(
        'kubesenseDashboard',
        'KubeSense Intelligence',
        vscode.ViewColumn.One,
        { enableScripts: true, retainContextWhenHidden: true }
      );
      panel.webview.html = getDashboardHtml(apiUrl());
    }),

    vscode.commands.registerCommand('kubesense.setApiUrl', async () => {
      const url = await vscode.window.showInputBox({
        prompt: 'KubeSense API URL',
        value: apiUrl(),
        placeHolder: 'http://kubesense-api:8080',
      });
      if (url) await config().update('apiUrl', url, vscode.ConfigurationTarget.Global);
    })
  );

  // ── Auto-score on YAML save ────────────────────────────────────────────────
  context.subscriptions.push(
    vscode.workspace.onDidSaveTextDocument(async (doc) => {
      if (!config().get<boolean>('autoScoreOnSave')) return;
      if (doc.languageId !== 'yaml') return;
      const manifest = parseK8sManifest(doc.getText());
      if (!manifest) return;
      await scoreAndShowRisk(manifest, apiUrl(), apiToken(), clusterId(), true);
    })
  );

  // ── Status bar item ────────────────────────────────────────────────────────
  const statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  statusBar.command = 'kubesense.openDashboard';
  statusBar.text = '$(lightning-bolt) KubeSense';
  statusBar.tooltip = 'Open KubeSense Intelligence Dashboard';
  statusBar.show();
  context.subscriptions.push(statusBar);

  vscode.window.showInformationMessage(`KubeSense Intelligence active — API: ${apiUrl()}`);
}

export function deactivate() {}

// ─── Tree view providers ──────────────────────────────────────────────────────

class KubeSenseExplorerProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
  private _onDidChangeTreeData = new vscode.EventEmitter<void>();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  constructor(
    private apiUrl: () => string,
    private apiToken: () => string,
    private clusterId: () => string
  ) {}

  refresh() { this._onDidChangeTreeData.fire(); }

  getTreeItem(item: vscode.TreeItem) { return item; }

  async getChildren(element?: vscode.TreeItem): Promise<vscode.TreeItem[]> {
    if (element) return [];

    try {
      const res = await apiGet(`${this.apiUrl()}/api/v1/clusters`, this.apiToken());
      const clusters: Array<{ id: string; node_count: number; status: string }> = res.clusters ?? [];

      if (clusters.length === 0) {
        const item = new vscode.TreeItem('No clusters connected');
        item.description = 'Check kubesense-agent deployment';
        return [item];
      }

      return clusters.map(c => {
        const item = new vscode.TreeItem(c.id, vscode.TreeItemCollapsibleState.None);
        item.description = `${c.node_count} nodes · ${c.status}`;
        item.iconPath     = new vscode.ThemeIcon(c.status === 'active' ? 'circle-filled' : 'circle-outline');
        item.contextValue = 'cluster';
        return item;
      });
    } catch (err) {
      const item = new vscode.TreeItem(`Cannot reach KubeSense API`);
      item.description = this.apiUrl();
      item.iconPath     = new vscode.ThemeIcon('warning');
      return [item];
    }
  }
}

class ChaosReadinessProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
  private _onDidChangeTreeData = new vscode.EventEmitter<void>();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  constructor(
    private apiUrl: () => string,
    private apiToken: () => string,
    private clusterId: () => string
  ) {}

  refresh() { this._onDidChangeTreeData.fire(); }
  getTreeItem(item: vscode.TreeItem) { return item; }

  getChildren(): vscode.TreeItem[] {
    const items = [
      { label: 'Score workloads',       desc: 'Run chaos readiness check',  icon: 'search' },
      { label: 'View findings',          desc: 'CIS checks per workload',    icon: 'list-unordered' },
      { label: 'Remediation guidance',  desc: 'Fix missing probes, PDBs',   icon: 'wrench' },
    ];
    return items.map(({ label, desc, icon }) => {
      const item = new vscode.TreeItem(label);
      item.description = desc;
      item.iconPath     = new vscode.ThemeIcon(icon);
      return item;
    });
  }
}

class PlaybookProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
  private _onDidChangeTreeData = new vscode.EventEmitter<void>();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  constructor(
    private apiUrl: () => string,
    private apiToken: () => string,
    private clusterId: () => string
  ) {}

  refresh() { this._onDidChangeTreeData.fire(); }
  getTreeItem(item: vscode.TreeItem) { return item; }

  async getChildren(): Promise<vscode.TreeItem[]> {
    if (!this.clusterId()) {
      const item = new vscode.TreeItem('Set kubesense.clusterId in settings');
      return [item];
    }
    try {
      const res = await apiGet(`${this.apiUrl()}/api/v1/clusters/${this.clusterId()}/playbooks`, this.apiToken());
      const playbooks: Array<{ id: string; failure_mode: string; overall_success_rate: number; data_points: number }> = res.playbooks ?? [];
      if (playbooks.length === 0) {
        const item = new vscode.TreeItem('No playbooks yet');
        item.description = 'Resolve incidents to build them';
        return [item];
      }
      return playbooks.map(pb => {
        const item = new vscode.TreeItem(pb.failure_mode);
        item.description = `${pb.data_points} incidents · ${Math.round(pb.overall_success_rate * 100)}% success`;
        item.iconPath     = new vscode.ThemeIcon(pb.overall_success_rate >= 0.8 ? 'check' : 'warning');
        return item;
      });
    } catch {
      return [new vscode.TreeItem('API unreachable')];
    }
  }
}

// ─── Risk scoring ─────────────────────────────────────────────────────────────

async function scoreAndShowRisk(
  manifest: K8sManifest,
  apiUrl: string,
  token: string,
  clusterId: string,
  silent = false,
) {
  try {
    const score = await apiPost(`${apiUrl}/api/v1/risk/score`, token, {
      cluster_id:    clusterId,
      resource_kind: manifest.kind,
      namespace:     manifest.metadata?.namespace ?? 'default',
      name:          manifest.metadata?.name ?? '',
      change_type:   'image_update',
    });

    const pct   = Math.round(score.raw_score * 100);
    const level: string = score.level;
    const emoji = level === 'critical' || level === 'high' ? '$(alert)' : '$(pass)';
    const msg   = `${emoji} KubeSense Risk: ${level.toUpperCase()} (${pct}%) — ${manifest.kind}/${manifest.metadata?.name}`;

    if (!silent || level === 'high' || level === 'critical') {
      const action = await (level === 'high' || level === 'critical'
        ? vscode.window.showWarningMessage(msg, 'Show details', 'Dismiss')
        : vscode.window.showInformationMessage(msg, 'Show details', 'Dismiss')
      );
      if (action === 'Show details') {
        const panel = vscode.window.createWebviewPanel(
          'kubesenseRisk',
          `Risk: ${manifest.metadata?.name}`,
          vscode.ViewColumn.Beside,
          { enableScripts: true }
        );
        panel.webview.html = getRiskDetailHtml(score, manifest);
      }
    }
  } catch (err) {
    if (!silent) vscode.window.showErrorMessage(`KubeSense API error: ${err}`);
  }
}

async function triggerInvestigation(manifest: K8sManifest, apiUrl: string, token: string, clusterId: string) {
  const name = manifest.metadata?.name;
  const ns   = manifest.metadata?.namespace ?? 'default';

  vscode.window.withProgress(
    { location: vscode.ProgressLocation.Notification, title: `KubeSense: Investigating ${name}...`, cancellable: false },
    async () => {
      try {
        const inv = await apiPost(`${apiUrl}/api/v1/investigations`, token, {
          cluster_id: clusterId,
          affected_resources: [{ kind: manifest.kind, namespace: ns, name }],
          async: false,
        });
        const grade      = inv.evidence_grade ?? '?';
        const confidence = Math.round((inv.confidence ?? 0) * 100);
        const rc         = inv.root_cause;
        const msg = rc
          ? `KubeSense RCA: ${rc.failure_mode} on ${rc.entity_name} (confidence ${confidence}%, grade ${grade})`
          : `KubeSense RCA complete: grade ${grade}, confidence ${confidence}%`;
        vscode.window.showInformationMessage(msg);
      } catch (err) {
        vscode.window.showErrorMessage(`KubeSense investigation failed: ${err}`);
      }
    }
  );
}

// ─── YAML parsing ──────────────────────────────────────────────────────────────

interface K8sManifest {
  apiVersion: string;
  kind: string;
  metadata?: { name?: string; namespace?: string };
}

function parseK8sManifest(text: string): K8sManifest | null {
  // Simple YAML header parse — no full YAML dependency needed
  const kindMatch = text.match(/^kind:\s*(\S+)/m);
  const nameMatch = text.match(/^\s+name:\s*(\S+)/m);
  const nsMatch   = text.match(/^\s+namespace:\s*(\S+)/m);
  const apiMatch  = text.match(/^apiVersion:\s*(\S+)/m);
  if (!kindMatch) return null;
  return {
    apiVersion: apiMatch?.[1] ?? '',
    kind:       kindMatch[1],
    metadata:   { name: nameMatch?.[1], namespace: nsMatch?.[1] },
  };
}

// ─── HTTP helpers ──────────────────────────────────────────────────────────────

async function apiGet(url: string, token: string): Promise<any> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (token) headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(url, { headers });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

async function apiPost(url: string, token: string, body: any): Promise<any> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (token) headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(url, { method: 'POST', headers, body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

// ─── Webview HTML ──────────────────────────────────────────────────────────────

function getDashboardHtml(apiUrl: string): string {
  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <style>
    body { font-family: var(--vscode-font-family); color: var(--vscode-foreground); padding: 20px; }
    h1   { font-size: 18px; margin-bottom: 4px; }
    p    { color: var(--vscode-descriptionForeground); font-size: 13px; }
    code { font-family: var(--vscode-editor-font-family); color: var(--vscode-textLink-foreground); }
  </style>
</head>
<body>
  <h1>KubeSense Intelligence Dashboard</h1>
  <p>Connected to: <code>${apiUrl}</code></p>
  <p>Use the sidebar panels to explore cluster intelligence, chaos readiness, and playbooks.</p>
  <p>Right-click any Kubernetes YAML file to score risk or trigger an investigation.</p>
</body>
</html>`;
}

function getRiskDetailHtml(score: any, manifest: K8sManifest): string {
  const levelColor: Record<string, string> = {
    low: '#3fb950', medium: '#d29922', high: '#f85149', critical: '#ff6b35',
  };
  const color = levelColor[score.level] ?? '#636e72';
  const factors = (score.factors ?? []).map((f: any) =>
    `<tr><td style="color:#8b949e;padding:4px 8px">${f.name.replace(/_/g, ' ')}</td>
     <td style="padding:4px 8px">
       <div style="height:6px;background:#21262d;border-radius:3px;width:120px">
         <div style="height:100%;background:${f.score>=0.7?'#f85149':f.score>=0.4?'#d29922':'#3fb950'};width:${f.score*100}%;border-radius:3px"></div>
       </div>
     </td>
     <td style="color:#e6edf3;padding:4px 8px;font-family:monospace">${Math.round(f.score*100)}%</td></tr>`
  ).join('');

  return `<!DOCTYPE html>
<html>
<head><meta charset="UTF-8">
<style>
  body{font-family:Inter,sans-serif;color:#e6edf3;background:#0d1117;padding:20px;font-size:13px}
  h1{margin-bottom:4px}p{color:#8b949e}table{border-collapse:collapse;width:100%}
</style>
</head>
<body>
  <h1 style="color:${color}">${score.level.toUpperCase()} Risk — ${Math.round(score.raw_score*100)}%</h1>
  <p>${manifest.kind}/${manifest.metadata?.namespace}/${manifest.metadata?.name}</p>
  <p style="margin-top:12px">${score.summary}</p>
  <h3 style="margin-top:20px;font-size:13px">Risk Factors</h3>
  <table>${factors}</table>
</body>
</html>`;
}
