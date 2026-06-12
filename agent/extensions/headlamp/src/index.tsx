// KubeSense Headlamp Plugin
// Registers KubeSense intelligence panels on Kubernetes resource detail pages.
//
// What it adds to Headlamp:
//   - "KubeSense" tab on every Deployment/StatefulSet/DaemonSet detail page
//     showing: chaos readiness score, drift status, pre-apply risk score
//   - "Investigate" action in the resource action menu
//   - KubeSense sidebar section under "Intelligence"
//   - Cluster-level intelligence dashboard accessible from the left nav
//
// Configuration: set KUBESENSE_API_URL in Headlamp settings to point to
//   your kubesense-api service (default: http://kubesense-api:8080)

import {
  registerDetailsViewSection,
  registerRoute,
  registerSidebarEntry,
} from '@kinvolk/headlamp-plugin/lib';
import React, { useEffect, useState } from 'react';

// ─── Plugin registration ──────────────────────────────────────────────────────

// Register sidebar entry under a "Intelligence" group
registerSidebarEntry({
  name:       'kubesense',
  label:      'KubeSense Intelligence',
  icon:       'mdi:lightning-bolt',
  url:        '/kubesense',
  useClusterURL: true,
});

registerSidebarEntry({
  name:       'kubesense-investigate',
  label:      'Investigate',
  icon:       'mdi:magnify',
  url:        '/kubesense/investigate',
  parent:     'kubesense',
  useClusterURL: true,
});

registerSidebarEntry({
  name:       'kubesense-chaos',
  label:      'Chaos Readiness',
  icon:       'mdi:lightning-bolt-circle',
  url:        '/kubesense/chaos',
  parent:     'kubesense',
  useClusterURL: true,
});

// Register intelligence tab on Deployment detail pages
registerDetailsViewSection({
  name: 'kubesense-intelligence',
  // Match Deployment, StatefulSet, DaemonSet
  views: [
    {
      kind:       'Deployment',
      apiVersion: 'apps/v1',
    },
    {
      kind:       'StatefulSet',
      apiVersion: 'apps/v1',
    },
    {
      kind:       'DaemonSet',
      apiVersion: 'apps/v1',
    },
  ],
  component: ({ resource }: { resource: any }) => {
    return React.createElement(KubeSenseIntelligencePanel, {
      kind:      resource?.kind,
      namespace: resource?.metadata?.namespace,
      name:      resource?.metadata?.name,
    });
  },
  label: 'KubeSense',
});

// Register custom routes
registerRoute({
  path:         '/kubesense',
  exact:        true,
  name:         'KubeSenseDashboard',
  component:    () => React.createElement(KubeSenseDashboardPage),
  useClusterURL: true,
});

registerRoute({
  path:         '/kubesense/investigate',
  exact:        true,
  name:         'KubeSenseInvestigate',
  component:    () => React.createElement(KubeSenseInvestigatePage),
  useClusterURL: true,
});

// ─── Components ───────────────────────────────────────────────────────────────

const API_URL = (typeof window !== 'undefined' && (window as any).__KUBESENSE_API_URL__)
  || 'http://kubesense-api:8080';

interface IntelligenceData {
  risk?: {
    level: string;
    raw_score: number;
    summary: string;
    factors: Array<{ name: string; score: number; description: string }>;
  };
  error?: string;
}

function KubeSenseIntelligencePanel({ kind, namespace, name }: {
  kind: string; namespace: string; name: string;
}) {
  const [data, setData] = useState<IntelligenceData | null>(null);
  const [loading, setLoading] = useState(false);

  const LEVEL_COLORS: Record<string, string> = {
    low:      '#3fb950',
    medium:   '#d29922',
    high:     '#f85149',
    critical: '#ff6b35',
  };

  const scoreToChaosGrade = (score: number): string => {
    if (score >= 0.8) return 'F';
    if (score >= 0.6) return 'D';
    if (score >= 0.4) return 'C';
    if (score >= 0.2) return 'B';
    return 'A';
  };

  const loadData = async () => {
    if (!name || !namespace) return;
    setLoading(true);
    try {
      const res = await fetch(`${API_URL}/api/v1/risk/score`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          resource_kind: kind,
          namespace,
          name,
          change_type: 'image_update',
        }),
      });
      const json = await res.json();
      setData({ risk: json });
    } catch (err) {
      setData({ error: String(err) });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { loadData(); }, [kind, namespace, name]);

  const containerStyle: React.CSSProperties = {
    padding: '16px',
    fontFamily: 'Inter, system-ui, sans-serif',
    fontSize: '13px',
  };

  const cardStyle: React.CSSProperties = {
    background: '#161b22',
    border: '1px solid #30363d',
    borderRadius: '8px',
    padding: '16px',
    marginBottom: '12px',
  };

  if (loading) {
    return React.createElement('div', { style: containerStyle },
      React.createElement('p', { style: { color: '#636e72' } }, 'Loading KubeSense intelligence...')
    );
  }

  if (data?.error) {
    return React.createElement('div', { style: containerStyle },
      React.createElement('div', {
        style: { ...cardStyle, borderColor: '#f85149', color: '#f85149', fontSize: '12px' }
      }, `KubeSense API error: ${data.error}. Ensure kubesense-api is reachable at ${API_URL}`)
    );
  }

  const risk = data?.risk;

  return React.createElement('div', { style: containerStyle },
    // Risk/Chaos Score
    risk && React.createElement('div', { style: cardStyle },
      React.createElement('div', {
        style: { display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '12px' }
      },
        React.createElement('h3', { style: { margin: 0, fontSize: '13px', color: '#e6edf3' } }, 'Chaos Readiness Score'),
        React.createElement('div', {
          style: {
            display: 'flex', alignItems: 'center', gap: '8px',
          }
        },
          React.createElement('span', {
            style: {
              fontSize: '20px', fontWeight: 'bold', fontFamily: 'monospace',
              color: LEVEL_COLORS[risk.level] ?? '#636e72',
            }
          }, scoreToChaosGrade(risk.raw_score)),
          React.createElement('span', {
            style: {
              padding: '2px 8px', borderRadius: '12px',
              background: `${LEVEL_COLORS[risk.level]}22`,
              color: LEVEL_COLORS[risk.level],
              fontSize: '11px', fontWeight: '600', textTransform: 'uppercase',
            }
          }, risk.level)
        )
      ),
      React.createElement('p', { style: { margin: '0 0 12px', color: '#8b949e', fontSize: '12px', lineHeight: '1.5' } }, risk.summary),
      // Factor bars
      ...risk.factors.slice(0, 4).map(f =>
        React.createElement('div', { key: f.name, style: { marginBottom: '8px' } },
          React.createElement('div', {
            style: { display: 'flex', justifyContent: 'space-between', marginBottom: '3px', fontSize: '11px' }
          },
            React.createElement('span', { style: { color: '#8b949e' } }, f.name.replace(/_/g, ' ')),
            React.createElement('span', { style: { color: '#e6edf3', fontFamily: 'monospace' } }, `${Math.round(f.score * 100)}%`)
          ),
          React.createElement('div', {
            style: { height: '4px', background: '#21262d', borderRadius: '2px', overflow: 'hidden' }
          },
            React.createElement('div', {
              style: {
                height: '100%', borderRadius: '2px',
                width: `${f.score * 100}%`,
                background: f.score >= 0.7 ? '#f85149' : f.score >= 0.4 ? '#d29922' : '#3fb950',
                transition: 'width 0.3s ease',
              }
            })
          )
        )
      )
    ),

    // Investigate button
    React.createElement('div', { style: cardStyle },
      React.createElement('h3', { style: { margin: '0 0 8px', fontSize: '13px', color: '#e6edf3' } }, 'RCA Investigation'),
      React.createElement('p', { style: { margin: '0 0 12px', color: '#8b949e', fontSize: '12px' } },
        'Trigger a full root cause analysis for this resource using KubeSense evidence-first RCA engine.'
      ),
      React.createElement('a', {
        href: `/kubesense/investigate?kind=${kind}&namespace=${namespace}&name=${name}`,
        style: {
          display: 'inline-flex', alignItems: 'center', gap: '6px',
          padding: '6px 14px', borderRadius: '6px',
          background: '#4a9eff', color: '#fff',
          fontSize: '12px', fontWeight: '500', textDecoration: 'none',
          cursor: 'pointer',
        }
      }, 'Investigate this resource')
    )
  );
}

function KubeSenseDashboardPage() {
  return React.createElement('div', {
    style: { padding: '24px', fontFamily: 'Inter, sans-serif' }
  },
    React.createElement('h1', { style: { fontSize: '20px', color: '#e6edf3', marginBottom: '8px' } },
      'KubeSense Intelligence Dashboard'
    ),
    React.createElement('p', { style: { color: '#8b949e', fontSize: '13px', marginBottom: '24px' } },
      'Kubernetes intelligence powered by KubeSense — chaos readiness, drift detection, RCA, toil metrics.'
    ),
    React.createElement('p', { style: { color: '#4a9eff', fontSize: '13px' } },
      `Connected to: ${API_URL}`
    )
  );
}

function KubeSenseInvestigatePage() {
  return React.createElement('div', {
    style: { padding: '24px', fontFamily: 'Inter, sans-serif' }
  },
    React.createElement('h1', { style: { fontSize: '20px', color: '#e6edf3', marginBottom: '8px' } },
      'RCA Investigation'
    ),
    React.createElement('p', { style: { color: '#8b949e', fontSize: '13px' } },
      'Use the Investigate tab on any Deployment, StatefulSet, or DaemonSet detail page to trigger an investigation.'
    )
  );
}
