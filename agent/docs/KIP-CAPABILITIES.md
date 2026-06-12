# KubeSense — Advanced Intelligence Capabilities
# APM, Anomaly Detection, Security, Cost, SLO, Log, Correlation, Remediation

---

## Intelligence Layer Map

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    KubeSense Intelligence Stack                                │
├─────────────────┬───────────────────┬────────────────────────────────────┤
│  OBSERVE        │  UNDERSTAND        │  ACT                               │
├─────────────────┼───────────────────┼────────────────────────────────────┤
│ • K8s events    │ APM: Golden Sigs  │ Evidence-based recommendations     │
│ • Topology      │ Anomaly Detection │ Ranked by historical success rate   │
│ • Health checks │ Security Analysis │ kubectl commands included           │
│ • Log patterns  │ Cost Efficiency   │ Blast radius classified             │
│ • Traces (OTel) │ SLO Tracking      │ Rollback commands included          │
│ • Metrics       │ RCA Engine        │ Confidence from outcome history     │
│ • Changes       │ Correlation       │                                     │
└─────────────────┴───────────────────┴────────────────────────────────────┘
```

---

## 1. APM — Application Performance Monitoring
File: `services/core/internal/apm/golden_signals.go`

### Golden Signals (RED + Saturation)
Every service tracked on 4 dimensions:
- **Rate**: requests per second
- **Errors**: error fraction (0–1) with absolute count
- **Duration**: p50 / p90 / p95 / p99 / p99.9 / max latency
- **Saturation**: active connections, queue depth, CPU throttle %

### Health Score (0–100)
Computed deterministically from signal deviations vs baseline:
- Error rate 0% = 0 penalty; 5% = −50 points; 10%+ = −80 points
- p99 latency >3σ above baseline = up to −40 points
- Request rate drop >50% = up to −30 points

### Service Dependency Map
- Edges discovered from: OpenTelemetry traces, service mesh telemetry,
  DNS resolution patterns, Kubernetes EndpointSlice watch
- EMA smoothing (α=0.3) prevents transient spikes from polluting the map
- Stale edges pruned after 30 minutes of no observation
- Confidence score per edge based on discovery source

### Performance Regression Detection
- Welford's online algorithm for numerically stable baseline learning
- Regression detected when signal deviates >3σ from 7-day baseline
- Correlated with recent deployments (change within 1h before regression = strong signal)
- Severity: critical (>100% regression), high (>50%), medium (>25%)

### Kafka Topics
- `kubesense.apm.golden-signals` — per-service metric snapshots (6 partitions, 3d retention)
- `kubesense.apm.regressions` — detected performance regressions
- `kubesense.apm.service-map` — compacted: latest service dependency map

---

## 2. Anomaly Detection
File: `services/core/internal/anomaly/detector.go`

### EWMA-based Per-Resource Detection
- One `EWMADetector` per (resource, signal) pair — zero config, self-learns
- Different α values per signal type:
  - Latency, error rate: α=0.3 (fast adaptation — immediate problems matter)
  - CPU, memory, disk: α=0.1 (slow adaptation — smooth out workload variance)
  - Restart count: threshold=2.0σ (more sensitive)
- Minimum 30 samples before flagging (no false alerts on new deployments)
- Auto-pruning of stale detectors (24h inactivity)

### Signals Monitored
cpu_usage | memory_usage | latency_p99 | error_rate | request_rate |
restart_count | disk_usage | network_rx_bytes | network_tx_bytes | connection_count

### Multi-Signal Correlation
- `AnomalyCorrelator`: groups anomalies across a cluster within a 5-minute window
- `GroupAnomalies`: identifies cases where ≥3 resources show simultaneous anomalies
- Likely source: resource with highest z-score in the group

### Kafka Topic
- `kubesense.anomalies` — all anomaly alerts (6 partitions, 7d retention)

---

## 3. Security Intelligence
File: `services/core/internal/security/analyzer.go`

### RBAC Analysis
| Rule | Severity | What it catches |
|---|---|---|
| RBAC_WILDCARD_ALL | Critical | resources=[*] verbs=[*] — equivalent to cluster-admin |
| RBAC_CLUSTER_ADMIN_BINDING | Critical | Direct cluster-admin binding to any subject |
| RBAC_POD_EXEC | High | pods/exec or pods/attach granted |
| RBAC_ESCALATION_VERB | Critical | escalate/bind/impersonate verbs |
| RBAC_SECRET_READ | High | Cluster-wide secret list/get/watch |

### Container Security
| Rule | Severity | CIS Benchmark |
|---|---|---|
| CONTAINER_PRIVILEGED | Critical | 5.2.1 |
| CONTAINER_RUNS_AS_ROOT | High | 5.2.6 |
| CONTAINER_ALLOW_PRIVESC | High | 5.2.5 |
| CONTAINER_WRITABLE_FS | Medium | 5.2.9 |
| CONTAINER_DANGEROUS_CAP_* | High | Various |
| CONTAINER_NO_SECURITY_CONTEXT | Medium | 5.2.6 |

### Pod-Level Security
- hostPID / hostNetwork / hostIPC detection (Critical/High)

### Network Policy Analysis
- Namespaces with zero NetworkPolicies → all traffic allowed
- Pods uncovered by any policy (selector gaps)
- Policies with empty From/To rules (allow-all)

### Secret Exposure
- Secrets as env vars (vs mounted files) — visible in process listings
- MITRE ATT&CK: Credential Access tactic

### Security Posture Score (0–100)
- −20 per critical finding, −8 per high, −3 per medium
- Grades: A (≥90), B (≥75), C (≥60), D (≥40), F (<40)

### Kafka Topics
- `kubesense.security.findings` — posture findings (30d retention, audit trail)
- `kubesense.security.runtime` — runtime anomalies (7d retention)

---

## 4. Cost Intelligence
File: `services/core/internal/cost/efficiency.go`

### Resource Efficiency Analysis
- p95 CPU and memory usage over 7 days vs requested values
- Efficiency score = actual_p95 / requested (per resource)
- **Idle detection**: CPU p95 < 5% of request over 24h
- **Over-provisioning**: combined efficiency < 30%
- Grades: A (≥80%), B (≥60%), C (≥40%), D (≥20%), F (<20%)

### Rightsizing Recommendations
- Recommended request = p95_usage × (1 + safety_margin%)
- Safety margin: 10–30% configurable (default 20%)
- Limit = 2× recommended request
- Values rounded to sensible increments (10m CPU, 64Mi memory)
- Monthly savings estimate at cloud list prices (configurable per cloud)

### Cost Attribution
- Per-namespace and per-team cost breakdown
- Monthly cost estimate from CPU core-hours + memory GiB-hours
- Top wasters ranked by estimated monthly waste

### Default Pricing (configurable)
- CPU: $0.048/core-hour (AWS c5.large equivalent)
- Memory: $0.006/GiB-hour
- Storage: $0.10/GiB-month (EBS gp3)

### Kafka Topics
- `kubesense.cost.efficiency` — compacted: latest efficiency per workload
- `kubesense.cost.recommendations` — rightsizing recommendations

---

## 5. SLO Tracking
File: `services/core/internal/slo/tracker.go`

### SLO Types
- **availability**: uptime fraction (e.g. 99.9%)
- **latency_p99**: p99 latency < threshold
- **error_rate**: error fraction < threshold
- **throughput**: requests/second > floor

### Error Budget Computation
- Rolling window evaluation (configurable: 7, 28, 30 days)
- Budget = window_minutes × (1 − target)
- Consumed = minutes where SLI was below target

### Burn Rate Alerting (Google SRE Model)
Based on multi-window burn rate alerting (SRE Workbook Chapter 5):
| Rule | Condition | Severity | Meaning |
|---|---|---|---|
| Fast burn | burn_rate_1h > 14.4 | **page** | Will exhaust 30d budget in 2 days |
| Medium burn | burn_rate_6h > 6.0 | **page** | Will exhaust budget in ~5 days |
| Slow burn | burn_rate_24h > 3.0 | **ticket** | Trending toward SLO breach |

### Budget Status
- **healthy**: >25% remaining, burn rate <1.5×
- **warning**: 5–25% remaining, or burn rate >3–6×
- **critical**: <5% remaining, or burn rate >6×
- **breached**: 0% remaining

### Kafka Topics
- `kubesense.slo.budgets` — compacted: latest budget per SLO
- `kubesense.slo.burn-rate-alerts` — burn rate alerts routed to AlertHub

---

## 6. Log Intelligence
File: `services/core/internal/log/analyzer.go`

### Log Template Extraction
Normalizes variable parts of log messages:
- UUIDs → `<UUID>`
- IP addresses and ports → `<IP>`
- Timestamps → `<TIMESTAMP>`
- File paths → `<PATH>`
- Long numbers → `<NUM>`
- Long quoted strings → `"<STRING>"`

Result: "Connection to 192.168.1.5:5432 failed after 3000ms"
→ "Connection to `<IP>` failed after `<NUM>`ms"

### Error Pattern Tracking
- Streaming ingestion: one `PatternTracker` per namespace
- Frequency statistics with sliding window (configurable, default 1h)
- **New FATAL/PANIC pattern** → immediate `critical` anomaly
- **New ERROR pattern** → `medium` anomaly
- Top-N patterns by frequency for diagnostic view

### Log Spike Detection
- EWMA-based (α=0.2) spike detector on error log volume
- Fires when error count/minute >3σ above baseline
- Minimum 20 samples before alerting

### Kafka Topics
- `kubesense.logs.patterns` — streaming pattern observations
- `kubesense.logs.anomalies` — log anomaly alerts

---

## 7. Cross-Signal Correlation
File: `services/core/internal/correlation/engine.go`

### Unified Incident Context
The `IncidentContext` is the single output that AlertHub consumes.
It fuses ALL intelligence layers into one structure:

```json
{
  "incident_id": "I-1234",
  "signals": [...],           // ALL signals from ALL layers
  "metric_signals": [...],    // grouped by source
  "log_signals": [...],
  "trace_signals": [...],
  "change_signals": [...],
  "performance_regression": {...},
  "anomalies_detected": [...],
  "slo_impact": {...},
  "security_findings": [...],
  "cost_impact": {...},
  "root_cause": {...},
  "recommended_actions": [...],  // ordered, with kubectl commands
  "evidence_grade": "B",         // A/B/C/D/F
  "confidence": 0.82
}
```

### Evidence Grading
| Grade | Requirements |
|---|---|
| A | All 6 signal sources present + root cause identified |
| B | 5 sources |
| C | 3–4 sources |
| D | 2 sources |
| F | 0–1 source |

### Kafka Topic
- `kubesense.correlation.incident-context` — consumed by AlertHub enrichment pipeline

---

## 8. Evidence-Based Remediation
File: `services/core/internal/remediation/engine.go`

### Scenarios Handled
1. **CrashLoopBackOff** — OOMKilled → increase limits; recent deploy → rollback; always → check logs
2. **NodeNotReady** — investigate → cordon+drain if pods affected
3. **High Error Rate** — recent deploy → rollback; always → check upstream deps

### Every Recommendation Includes
- Specific `kubectl` command (ready to execute)
- Rationale citing exact evidence items
- Blast radius: `none | pod | service | cluster`
- `is_reversible: true/false` + rollback command
- Historical success rate from `OutcomeStore`
- Expected resolution time (median from history)

### Outcome Learning
- `OutcomeStore` tracks every executed recommendation outcome
- Success rate improves with each resolved incident
- Low-sample actions default to 70% prior (conservative)

### Kafka Topics
- `kubesense.remediation.recommendations` — recommendations routed to AlertHub
- `kubesense.remediation.outcomes` — operator feedback (resolved/unresolved) for learning

---

## 9. Complete Capability Matrix

| Capability | Status | File | Kafka Topic |
|---|---|---|---|
| Topology Discovery | ✅ | agent/topology/graph.go | kubesense.events.topology |
| Health Detection | ✅ | agent/health/detector.go | kubesense.events.health |
| Config Validation | ✅ | core/config/validator.go | kubesense.config.violations |
| Forecasting | ✅ | core/forecast/engine.go | kubesense.forecasts |
| RCA Engine | ✅ | core/rca/engine.go | kubesense.investigations.results |
| APM Golden Signals | ✅ | core/apm/golden_signals.go | kubesense.apm.golden-signals |
| Service Dependency Map | ✅ | core/apm/golden_signals.go | kubesense.apm.service-map |
| Regression Detection | ✅ | core/apm/golden_signals.go | kubesense.apm.regressions |
| Anomaly Detection | ✅ | core/anomaly/detector.go | kubesense.anomalies |
| Multi-Signal Anomaly | ✅ | core/anomaly/detector.go | kubesense.anomalies |
| Security RBAC Analysis | ✅ | core/security/analyzer.go | kubesense.security.findings |
| Container Security | ✅ | core/security/analyzer.go | kubesense.security.findings |
| Network Policy Analysis | ✅ | core/security/analyzer.go | kubesense.security.findings |
| Secret Exposure | ✅ | core/security/analyzer.go | kubesense.security.findings |
| Cost Efficiency | ✅ | core/cost/efficiency.go | kubesense.cost.efficiency |
| Rightsizing | ✅ | core/cost/efficiency.go | kubesense.cost.recommendations |
| SLO Tracking | ✅ | core/slo/tracker.go | kubesense.slo.budgets |
| Burn Rate Alerting | ✅ | core/slo/tracker.go | kubesense.slo.burn-rate-alerts |
| Log Pattern Detection | ✅ | core/log/analyzer.go | kubesense.logs.patterns |
| Log Spike Detection | ✅ | core/log/analyzer.go | kubesense.logs.anomalies |
| Cross-Signal Correlation | ✅ | core/correlation/engine.go | kubesense.correlation.incident-context |
| Remediation Recommendations | ✅ | core/remediation/engine.go | kubesense.remediation.recommendations |
| Outcome Learning | ✅ | core/remediation/engine.go | kubesense.remediation.outcomes |

---

## 10. What Makes KubeSense GOAT

| Feature | Dynatrace Davis | Datadog Watchdog | KubeSense |
|---|---|---|---|
| Evidence-first RCA | Deterministic chains only | Correlation only | ✅ Multi-source + hypothesis rejection |
| APM without agent | ❌ OneAgent required | Agent required | ✅ OTel + service mesh |
| Security posture | Limited | Limited | ✅ CIS benchmarks + MITRE ATT&CK |
| Cost intelligence | Add-on | Add-on | ✅ Built-in, per workload |
| SLO burn rate alerts | Enterprise only | Enterprise only | ✅ Google SRE model, open |
| Log intelligence | Expensive (Grail) | Expensive | ✅ In-platform, pattern extraction |
| Remediation commands | Suggestions only | Suggestions only | ✅ kubectl commands + rollback |
| Outcome learning | Proprietary | Proprietary | ✅ Open, improves with each incident |
| Cross-signal correlation | Davis AI (opaque) | Watchdog (opaque) | ✅ Transparent evidence grades |
| Kafka-native | ❌ | ❌ | ✅ Reuses existing Strimzi cluster |
| LLM role | Decisions (risky) | Decisions (risky) | ✅ Narrator only — never decides |
| Open model | ❌ Proprietary | ❌ Proprietary | ✅ Evidence citations on every output |
