// Security view — security posture overview.
import { Shield, AlertTriangle, CheckCircle } from 'lucide-react';
import clsx from 'clsx';

const SECURITY_CHECKS = [
  { rule: 'RBAC_WILDCARD_ALL',        severity: 'critical', description: 'ClusterRole grants resources=[*] verbs=[*]' },
  { rule: 'CONTAINER_PRIVILEGED',     severity: 'critical', description: 'Container running in privileged mode' },
  { rule: 'RBAC_POD_EXEC',            severity: 'high',     description: 'pods/exec or pods/attach granted to service account' },
  { rule: 'SECRET_ENV_VAR',           severity: 'high',     description: 'Secret mounted as environment variable (visible in process list)' },
  { rule: 'CONTAINER_RUNS_AS_ROOT',   severity: 'high',     description: 'Container running as root user' },
  { rule: 'NETWORK_POLICY_MISSING',   severity: 'medium',   description: 'Namespace has no NetworkPolicy — all traffic allowed' },
  { rule: 'IMAGE_NO_TAG',             severity: 'medium',   description: 'Container image has no version tag (:latest or untagged)' },
  { rule: 'NO_SECURITY_CONTEXT',      severity: 'medium',   description: 'Container has no securityContext defined' },
];

export default function Security() {
  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="max-w-4xl mx-auto space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold text-white flex items-center gap-2">
              <Shield size={18} className="text-brand" />
              Security Posture
            </h1>
            <p className="text-xs text-gray-500 mt-0.5">
              CIS Kubernetes Benchmark · MITRE ATT&CK · RBAC analysis · Container security
            </p>
          </div>
        </div>

        {/* Score card */}
        <div className="grid grid-cols-4 gap-3">
          {[
            { label: 'Critical', count: 0, cls: 'badge-danger' },
            { label: 'High',     count: 0, cls: 'badge-critical' },
            { label: 'Medium',   count: 0, cls: 'badge-warn' },
            { label: 'Low',      count: 0, cls: 'badge-info' },
          ].map(({ label, count, cls }) => (
            <div key={label} className="card p-3 text-center">
              <div className={clsx('text-2xl font-bold font-mono', cls.replace('badge-', 'text-'))}>{count}</div>
              <div className="text-xs text-gray-500 mt-0.5">{label}</div>
            </div>
          ))}
        </div>

        {/* Rules reference */}
        <div className="card p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">Active Security Rules</h2>
          <div className="space-y-2">
            {SECURITY_CHECKS.map(check => (
              <div key={check.rule} className="flex items-start gap-3 py-2 px-3 rounded hover:bg-surface-overlay">
                <span className={clsx('badge flex-shrink-0 mt-0.5', {
                  'badge-danger': check.severity === 'critical',
                  'badge-critical': check.severity === 'critical',
                  'badge-warn': check.severity === 'medium',
                  'badge-info': check.severity === 'high',
                })}>
                  {check.severity}
                </span>
                <div className="min-w-0">
                  <div className="text-xs font-mono text-gray-300">{check.rule}</div>
                  <div className="text-xs text-gray-500 mt-0.5">{check.description}</div>
                </div>
                <CheckCircle size={12} className="text-ok flex-shrink-0 mt-0.5 opacity-50" title="No violations found" />
              </div>
            ))}
          </div>
        </div>

        <div className="card p-4 text-xs text-gray-500">
          <p className="mb-1 font-medium text-gray-400">Connect to a live cluster</p>
          The security analyzer runs continuously on all Deployments, RBAC objects, and
          NetworkPolicies via the admission webhook and the background security scanner.
          Findings are published to <code className="font-mono text-teal">kubesense.security.findings</code>.
        </div>
      </div>
    </div>
  );
}
