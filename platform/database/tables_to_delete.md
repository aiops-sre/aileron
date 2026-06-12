# Tables to Delete After Migration to schema_v2

All tables listed here are **superseded** by schema_v2. Drop them only after:

1. The migration plan phases 1–10 are complete.
2. The application has run in production for **≥ 7 days** without query errors against the old table names.
3. Pre-drop: verify the row counts in the replacement tables match the originals.

---

## Group 1 — Correlation Tables (Replaced by `correlation_results`)

These four tables are fully consolidated into the partitioned `correlation_results` table. The `alert_correlations` name is preserved as a VIEW so no Go code changes are needed.

| Table                        | Replaced By                  | Why                                                                |
|------------------------------|------------------------------|--------------------------------------------------------------------|
| `alert_correlations`         | VIEW `alert_correlations`    | Original table becomes a view over `correlation_results`           |
| `ai_correlation_results`     | `correlation_results`        | `correlation_type = 'ai_semantic'`, model stored in `metadata`     |
| `davis_ai_correlations`      | `correlation_results`        | `correlation_type = 'davis_ai'`, problem data in `metadata`        |
| `pipeline_correlation_results` | `correlation_results`      | `correlation_type = 'pipeline'`, strategy scores in `strategy_weights` |

```sql
-- Drop after 7-day soak (alert_correlations is now a VIEW, not a table)
DROP TABLE IF EXISTS ai_correlation_results   CASCADE;
DROP TABLE IF EXISTS davis_ai_correlations    CASCADE;
DROP TABLE IF EXISTS pipeline_correlation_results CASCADE;
-- Note: alert_correlations is already replaced by a VIEW in Phase 6.
-- If the old table was renamed during migration, drop the renamed version:
DROP TABLE IF EXISTS alert_correlations_old   CASCADE;
```

---

## Group 2 — Duplicate/Fragmented Topology Tables

These tables were created across multiple migration files and represent the same topology concept at different levels of completeness. `topology_entities`, `topology_management_zones`, `topology_sources`, and `topology_sync_log` in schema_v2 are the canonical replacements. Relationship data moves to Neo4j.

| Table                          | Replaced By                       | Why                                                           |
|--------------------------------|-----------------------------------|---------------------------------------------------------------|
| `dynatrace_topology`           | `topology_entities`               | Dynatrace-specific table subsumed by generic entity model     |
| `dynatrace_relationships`      | Neo4j graph                       | Relationship edges belong in Neo4j, not PostgreSQL            |
| `dynatrace_management_zones`   | `topology_management_zones`       | Direct rename with added fields                               |
| `topology_snapshots`           | `topology_sync_log`               | Replaced by structured sync log per source                    |
| `topology_config`              | `topology_sources`                | Config per source replaces single config table                |
| `k8s_topology_snapshots`       | `topology_sync_log`               | K8s snapshots are a source type, not a separate table         |
| `service_dependencies`         | Neo4j graph                       | Dependency graph belongs in Neo4j                             |
| `infrastructure_correlations`  | `correlation_results`             | Infrastructure correlation is a correlation_type              |

```sql
DROP TABLE IF EXISTS dynatrace_topology          CASCADE;
DROP TABLE IF EXISTS dynatrace_relationships     CASCADE;
DROP TABLE IF EXISTS dynatrace_management_zones  CASCADE;
DROP TABLE IF EXISTS topology_snapshots          CASCADE;
DROP TABLE IF EXISTS topology_config             CASCADE;
DROP TABLE IF EXISTS k8s_topology_snapshots      CASCADE;
DROP TABLE IF EXISTS service_dependencies        CASCADE;
DROP TABLE IF EXISTS infrastructure_correlations CASCADE;
```

---

## Group 3 — Fragmented Observability Tables

These were created in `migrations/observability_monitoring.sql` and `migrations/timescaledb.sql`. The metrics/health data belongs in Prometheus/Grafana, not PostgreSQL. The schema_v2 `service_health_checks` table covers the health-check use case.

| Table                     | Replaced By                    | Why                                                                |
|---------------------------|--------------------------------|--------------------------------------------------------------------|
| `observability_metrics`   | Prometheus/Grafana             | Time-series data should not live in OLTP PostgreSQL               |
| `observability_logs`      | Loki/ELK                       | Log storage is not PostgreSQL's job                               |
| `observability_incidents` | `incidents`                    | Duplicate of the main incidents table                              |
| `observability_anomalies` | `correlation_results`          | Anomaly = correlation_type 'temporal'                              |
| `real_time_metrics`       | Prometheus                     | Real-time metrics do not belong in OLTP                           |
| `time_series_metrics`     | Prometheus                     | TimescaleDB-specific; overkill for this use case                  |
| `capacity_metrics`        | Prometheus                     | Same rationale                                                    |
| `metrics`                 | Prometheus                     | Generic metrics table with no consumers                           |
| `sla_metrics`             | `slo_definitions`+`slo_violations` | SLO tables subsume the SLA concept                            |
| `sli_measurements`        | `slo_violations`               | SLI data lives in slo_violations.current_value                    |
| `service_metrics`         | Prometheus                     | Per-service metrics belong in Prometheus                          |
| `error_budget_tracking`   | `slo_violations`               | Error budget is derived from slo_violations                       |
| `performance_baselines`   | `historical_patterns`          | Baseline patterns live in historical_patterns                     |
| `synthetic_checks`        | `service_health_checks`        | Active probes are a type of health check                          |
| `synthetic_check_results` | `service_health_checks`        | Results belong in the partitioned health check table              |
| `health_checks`           | `service_health_checks`        | Direct replacement                                                 |
| `service_status_history`  | `service_health_checks`        | Status history is the health check log                            |

```sql
DROP TABLE IF EXISTS observability_metrics   CASCADE;
DROP TABLE IF EXISTS observability_logs      CASCADE;
DROP TABLE IF EXISTS observability_incidents CASCADE;
DROP TABLE IF EXISTS observability_anomalies CASCADE;
DROP TABLE IF EXISTS real_time_metrics       CASCADE;
DROP TABLE IF EXISTS time_series_metrics     CASCADE;
DROP TABLE IF EXISTS capacity_metrics        CASCADE;
DROP TABLE IF EXISTS metrics                 CASCADE;
DROP TABLE IF EXISTS sla_metrics             CASCADE;
DROP TABLE IF EXISTS sli_measurements        CASCADE;
DROP TABLE IF EXISTS service_metrics         CASCADE;
DROP TABLE IF EXISTS error_budget_tracking   CASCADE;
DROP TABLE IF EXISTS performance_baselines   CASCADE;
DROP TABLE IF EXISTS synthetic_checks        CASCADE;
DROP TABLE IF EXISTS synthetic_check_results CASCADE;
DROP TABLE IF EXISTS health_checks           CASCADE;
DROP TABLE IF EXISTS service_status_history  CASCADE;
```

---

## Group 4 — Duplicate Auth/OAuth Tables

Multiple migration files created overlapping auth tables. The schema_v2 `auth_providers` and `user_sessions` tables are the canonical single source of truth.

| Table                   | Replaced By             | Why                                                       |
|-------------------------|-------------------------|-----------------------------------------------------------|
| `oauth_settings`        | `auth_providers`        | OAuth config is a provider type                           |
| `oauth2_providers`      | `auth_providers`        | Duplicate table, different naming                         |
| `oauth_token_audit`     | `audit_logs`            | Token events are audit log entries                        |
| `mas_auth_logs`         | `audit_logs`            | MAS auth events are audit log entries                     |
| `mas_config`            | `auth_providers`        | MAS is a provider type with config JSONB                  |
| `mas_group_mappings`    | `auth_providers.config` | Group mappings stored as config JSON                      |
| `user_mas_groups`       | `users.metadata`        | User's group membership stored in user metadata           |
| `auth_attempts`         | `audit_logs`            | Failed login attempts are security audit events           |
| `authorization_checks`  | `audit_logs`            | Authz checks are audit log entries                        |
| `data_access_logs`      | `audit_logs`            | Data access is a category of audit log                    |

```sql
DROP TABLE IF EXISTS oauth_settings       CASCADE;
DROP TABLE IF EXISTS oauth2_providers     CASCADE;
DROP TABLE IF EXISTS oauth_token_audit    CASCADE;
DROP TABLE IF EXISTS mas_auth_logs        CASCADE;
DROP TABLE IF EXISTS mas_config           CASCADE;
DROP TABLE IF EXISTS mas_group_mappings   CASCADE;
DROP TABLE IF EXISTS user_mas_groups      CASCADE;
DROP TABLE IF EXISTS auth_attempts        CASCADE;
DROP TABLE IF EXISTS authorization_checks CASCADE;
DROP TABLE IF EXISTS data_access_logs     CASCADE;
```

---

## Group 5 — Agentic / AI Experiment Tables

Created speculatively in various migration files. No active Go code queries these tables for user-facing features.

| Table                      | Replaced By                   | Why                                                             |
|----------------------------|-------------------------------|-----------------------------------------------------------------|
| `agentic_tasks`            | —                             | No production code paths; speculative                           |
| `agent_messages`           | —                             | No production code paths; speculative                           |
| `ai_correlation_feedback`  | `llm_feedback`                | Unified feedback table in schema_v2                             |
| `feedback_loops`           | `llm_feedback`                | Same concept, different table                                   |
| `nlp_analysis`             | `correlation_results.metadata`| NLP output stored as metadata in correlation_results            |
| `ml_models`                | `ai_models`                   | Renamed and cleaned up in schema_v2                             |
| `predictions`              | `correlation_results`         | Predictions are a correlation result type                       |
| `anomaly_detections`       | `correlation_results`         | Anomaly detection result = correlation_type 'temporal'          |
| `ai_model_performance`     | `ai_models.metadata`          | Performance metrics stored in ai_models config                  |
| `pattern_library`          | `historical_patterns`         | Direct replacement                                              |
| `distributed_traces`       | Jaeger/Tempo                  | Trace data belongs in a dedicated tracing backend               |

```sql
DROP TABLE IF EXISTS agentic_tasks           CASCADE;
DROP TABLE IF EXISTS agent_messages          CASCADE;
DROP TABLE IF EXISTS ai_correlation_feedback CASCADE;
DROP TABLE IF EXISTS feedback_loops          CASCADE;
DROP TABLE IF EXISTS nlp_analysis            CASCADE;
DROP TABLE IF EXISTS ml_models               CASCADE;
DROP TABLE IF EXISTS predictions             CASCADE;
DROP TABLE IF EXISTS anomaly_detections      CASCADE;
DROP TABLE IF EXISTS ai_model_performance    CASCADE;
DROP TABLE IF EXISTS pattern_library         CASCADE;
DROP TABLE IF EXISTS distributed_traces      CASCADE;
```

---

## Group 6 — Config Management Experiment Tables

Created in `migrations/phase1_event_driven_infrastructure.sql`. Not wired to any active Go API handlers.

| Table                     | Replaced By         | Why                                                   |
|---------------------------|---------------------|-------------------------------------------------------|
| `config_namespaces`       | —                   | Config management feature not yet shipped             |
| `config_entries`          | —                   | Not referenced by application code                    |
| `config_schemas`          | —                   | Not referenced by application code                    |
| `config_validators`       | —                   | Not referenced by application code                    |
| `config_history`          | `audit_logs`        | Config change history is an audit log entry           |
| `config_deployments`      | —                   | Not referenced by application code                    |
| `config_templates`        | —                   | Not referenced by application code                    |
| `config_locks`            | —                   | Not referenced by application code                    |
| `config_approvals`        | —                   | Not referenced by application code                    |
| `config_approval_votes`   | —                   | Not referenced by application code                    |
| `config_dependencies`     | —                   | Not referenced by application code                    |
| `config_notifications`    | `notification_log`  | Notifications go through the notification pipeline    |
| `config_exports`          | —                   | Not referenced by application code                    |
| `config_cost_tracking`    | —                   | Not referenced by application code                    |

```sql
DROP TABLE IF EXISTS config_namespaces       CASCADE;
DROP TABLE IF EXISTS config_entries          CASCADE;
DROP TABLE IF EXISTS config_schemas          CASCADE;
DROP TABLE IF EXISTS config_validators       CASCADE;
DROP TABLE IF EXISTS config_history          CASCADE;
DROP TABLE IF EXISTS config_deployments      CASCADE;
DROP TABLE IF EXISTS config_templates        CASCADE;
DROP TABLE IF EXISTS config_locks            CASCADE;
DROP TABLE IF EXISTS config_approvals        CASCADE;
DROP TABLE IF EXISTS config_approval_votes   CASCADE;
DROP TABLE IF EXISTS config_dependencies     CASCADE;
DROP TABLE IF EXISTS config_notifications    CASCADE;
DROP TABLE IF EXISTS config_exports          CASCADE;
DROP TABLE IF EXISTS config_cost_tracking    CASCADE;
```

---

## Group 7 — Integration Provider Experiment Tables

Created in `migrations/integrations.sql`. Overlap with the core `integrations` table in schema_v2.

| Table                      | Replaced By           | Why                                                  |
|----------------------------|-----------------------|------------------------------------------------------|
| `provider_configurations`  | `integrations`        | Integrations table covers all providers              |
| `provider_specs`           | `integrations.config` | Spec stored as config JSONB                          |
| `provider_data_mappings`   | `integrations.config` | Field mappings stored as config JSONB                |
| `provider_metrics`         | `audit_logs`          | Provider performance is an operational audit concern |
| `provider_sync_logs`       | `audit_logs`          | Sync events are audit log entries                    |
| `provider_webhooks`        | `webhooks`            | All webhooks use the unified webhooks table          |
| `integration_performance`  | `audit_logs`          | Integration metrics are audit concerns               |
| `alert_bursts`             | `correlation_clusters`| Burst detection is a correlation cluster type        |
| `alert_storms`             | `correlation_clusters`| Storm detection is a correlation cluster type        |
| `alert_correlation_events` | `correlation_results` | Events are rows in correlation_results               |
| `alert_stream_events`      | `alert_history`       | Stream events are alert history entries              |
| `alert_metrics`            | `audit_logs`          | Alert metrics are operational audit data             |
| `event_stream_log`         | `audit_logs`          | Event stream belongs in audit logs                   |

```sql
DROP TABLE IF EXISTS provider_configurations  CASCADE;
DROP TABLE IF EXISTS provider_specs           CASCADE;
DROP TABLE IF EXISTS provider_data_mappings   CASCADE;
DROP TABLE IF EXISTS provider_metrics         CASCADE;
DROP TABLE IF EXISTS provider_sync_logs       CASCADE;
DROP TABLE IF EXISTS provider_webhooks        CASCADE;
DROP TABLE IF EXISTS integration_performance  CASCADE;
DROP TABLE IF EXISTS alert_bursts             CASCADE;
DROP TABLE IF EXISTS alert_storms             CASCADE;
DROP TABLE IF EXISTS alert_correlation_events CASCADE;
DROP TABLE IF EXISTS alert_stream_events      CASCADE;
DROP TABLE IF EXISTS alert_metrics            CASCADE;
DROP TABLE IF EXISTS event_stream_log         CASCADE;
```

---

## Group 8 — Compliance/Security Experiment Tables

Overlap with the core `audit_logs` and `security_events` tables in schema_v2.

| Table                      | Replaced By           | Why                                                       |
|----------------------------|-----------------------|-----------------------------------------------------------|
| `compliance_frameworks`    | `compliance_reports`  | Schema_v2 uses a single compliance_reports table          |
| `compliance_policies`      | `compliance_reports`  | Policies fold into reports metadata                       |
| `compliance_violations`    | `security_events`     | Violations are security events with type=compliance       |
| `audit_configuration`      | `audit_logs`          | Config audit events are audit log entries                 |
| `security_incidents`       | `incidents`           | Security incidents use the main incidents table           |
| `vulnerability_findings`   | `security_events`     | Vulnerability = security event with type=vulnerability    |

```sql
DROP TABLE IF EXISTS compliance_frameworks  CASCADE;
DROP TABLE IF EXISTS compliance_policies    CASCADE;
DROP TABLE IF EXISTS compliance_violations  CASCADE;
DROP TABLE IF EXISTS audit_configuration    CASCADE;
DROP TABLE IF EXISTS security_incidents     CASCADE;
DROP TABLE IF EXISTS vulnerability_findings CASCADE;
```

---

## Group 9 — Service Catalog Experiments

| Table                       | Replaced By               | Why                                               |
|-----------------------------|---------------------------|---------------------------------------------------|
| `services`                  | `topology_entities`       | Services are entity_type='service' in topology    |
| `service_catalog`           | `topology_entities`       | Service catalog = topology entity registry        |
| `service_discovery_sources` | `topology_sources`        | Discovery sources = topology sources              |
| `dependency_health`         | `service_health_checks`   | Health per dependency is a health check entry     |
| `k8s_service_accounts`      | `k8s_cluster_configs`     | K8s SA config lives in cluster config JSONB       |
| `metric_exporters`          | `topology_sources`        | Exporters are topology source type='prometheus'   |
| `metric_retention_policies` | —                         | Handled by Prometheus retention config            |

```sql
DROP TABLE IF EXISTS services                  CASCADE;
DROP TABLE IF EXISTS service_catalog           CASCADE;
DROP TABLE IF EXISTS service_discovery_sources CASCADE;
DROP TABLE IF EXISTS dependency_health         CASCADE;
DROP TABLE IF EXISTS k8s_service_accounts      CASCADE;
DROP TABLE IF EXISTS metric_exporters          CASCADE;
DROP TABLE IF EXISTS metric_retention_policies CASCADE;
```

---

## Group 10 — Misc Duplicates

| Table                        | Replaced By                   | Why                                                  |
|------------------------------|-------------------------------|------------------------------------------------------|
| `enhanced_correlation_rules` | `correlation_rules`           | Schema_v2 correlation_rules is the enhanced version  |
| `rca_investigations`         | `incidents.metadata`          | RCA data stored as incident metadata                 |
| `oncall_schedule_entries`    | `oncall_shifts`               | Duplicate concept                                    |
| `workflow_templates`         | `workflows`                   | Templates are workflows with template flag           |
| `workflow_schedules`         | `workflows.config`            | Schedule config is in workflow config JSONB          |
| `workflow_triggers`          | `workflows.trigger_type`      | Trigger type is a column, not a separate table       |
| `workflow_permissions`       | `role_permissions`            | Workflow-level permissions use RBAC framework        |
| `netapp_configs`             | `k8s_cluster_configs`         | Storage configs live in cluster config JSONB         |

```sql
DROP TABLE IF EXISTS enhanced_correlation_rules CASCADE;
DROP TABLE IF EXISTS rca_investigations          CASCADE;
DROP TABLE IF EXISTS oncall_schedule_entries     CASCADE;
DROP TABLE IF EXISTS workflow_templates          CASCADE;
DROP TABLE IF EXISTS workflow_schedules          CASCADE;
DROP TABLE IF EXISTS workflow_triggers           CASCADE;
DROP TABLE IF EXISTS workflow_permissions        CASCADE;
DROP TABLE IF EXISTS netapp_configs              CASCADE;
```

---

## Migration Partition Leftovers

After Phase 4 of the migration plan, the `_old` renamed tables must also be dropped:

```sql
DROP TABLE IF EXISTS alerts_old           CASCADE;
DROP TABLE IF EXISTS audit_logs_old       CASCADE;
DROP TABLE IF EXISTS notification_log_old CASCADE;
DROP TABLE IF EXISTS incident_timeline_old CASCADE;
```

---

## Total Tables to Delete: ~90

Run the complete drop sequence after 7-day soak in this order (respecting FK dependencies):

1. Groups 3–10 (no FKs from schema_v2 tables point to these)
2. Group 2 (topology)
3. Group 1 (correlation old tables)
4. Migration leftover `_old` tables

The complete atomic drop script:

```sql
-- Execute after 7-day production soak. Requires: all schema_v2 tables healthy.
BEGIN;

-- Group 1: correlation
DROP TABLE IF EXISTS ai_correlation_results         CASCADE;
DROP TABLE IF EXISTS davis_ai_correlations          CASCADE;
DROP TABLE IF EXISTS pipeline_correlation_results   CASCADE;
DROP TABLE IF EXISTS alert_correlations_old         CASCADE;

-- Group 2: topology
DROP TABLE IF EXISTS dynatrace_topology             CASCADE;
DROP TABLE IF EXISTS dynatrace_relationships        CASCADE;
DROP TABLE IF EXISTS dynatrace_management_zones     CASCADE;
DROP TABLE IF EXISTS topology_snapshots             CASCADE;
DROP TABLE IF EXISTS topology_config                CASCADE;
DROP TABLE IF EXISTS k8s_topology_snapshots         CASCADE;
DROP TABLE IF EXISTS service_dependencies           CASCADE;
DROP TABLE IF EXISTS infrastructure_correlations    CASCADE;

-- Group 3: observability
DROP TABLE IF EXISTS observability_metrics          CASCADE;
DROP TABLE IF EXISTS observability_logs             CASCADE;
DROP TABLE IF EXISTS observability_incidents        CASCADE;
DROP TABLE IF EXISTS observability_anomalies        CASCADE;
DROP TABLE IF EXISTS real_time_metrics              CASCADE;
DROP TABLE IF EXISTS time_series_metrics            CASCADE;
DROP TABLE IF EXISTS capacity_metrics               CASCADE;
DROP TABLE IF EXISTS metrics                        CASCADE;
DROP TABLE IF EXISTS sla_metrics                    CASCADE;
DROP TABLE IF EXISTS sli_measurements               CASCADE;
DROP TABLE IF EXISTS service_metrics                CASCADE;
DROP TABLE IF EXISTS error_budget_tracking          CASCADE;
DROP TABLE IF EXISTS performance_baselines          CASCADE;
DROP TABLE IF EXISTS synthetic_checks               CASCADE;
DROP TABLE IF EXISTS synthetic_check_results        CASCADE;
DROP TABLE IF EXISTS health_checks                  CASCADE;
DROP TABLE IF EXISTS service_status_history         CASCADE;

-- Group 4: auth
DROP TABLE IF EXISTS oauth_settings                 CASCADE;
DROP TABLE IF EXISTS oauth2_providers               CASCADE;
DROP TABLE IF EXISTS oauth_token_audit              CASCADE;
DROP TABLE IF EXISTS mas_auth_logs                  CASCADE;
DROP TABLE IF EXISTS mas_config                     CASCADE;
DROP TABLE IF EXISTS mas_group_mappings             CASCADE;
DROP TABLE IF EXISTS user_mas_groups                CASCADE;
DROP TABLE IF EXISTS auth_attempts                  CASCADE;
DROP TABLE IF EXISTS authorization_checks           CASCADE;
DROP TABLE IF EXISTS data_access_logs               CASCADE;

-- Group 5: agentic/AI experiments
DROP TABLE IF EXISTS agentic_tasks                  CASCADE;
DROP TABLE IF EXISTS agent_messages                 CASCADE;
DROP TABLE IF EXISTS ai_correlation_feedback        CASCADE;
DROP TABLE IF EXISTS feedback_loops                 CASCADE;
DROP TABLE IF EXISTS nlp_analysis                   CASCADE;
DROP TABLE IF EXISTS ml_models                      CASCADE;
DROP TABLE IF EXISTS predictions                    CASCADE;
DROP TABLE IF EXISTS anomaly_detections             CASCADE;
DROP TABLE IF EXISTS ai_model_performance           CASCADE;
DROP TABLE IF EXISTS pattern_library                CASCADE;
DROP TABLE IF EXISTS distributed_traces             CASCADE;

-- Group 6: config management
DROP TABLE IF EXISTS config_namespaces              CASCADE;
DROP TABLE IF EXISTS config_entries                 CASCADE;
DROP TABLE IF EXISTS config_schemas                 CASCADE;
DROP TABLE IF EXISTS config_validators              CASCADE;
DROP TABLE IF EXISTS config_history                 CASCADE;
DROP TABLE IF EXISTS config_deployments             CASCADE;
DROP TABLE IF EXISTS config_templates               CASCADE;
DROP TABLE IF EXISTS config_locks                   CASCADE;
DROP TABLE IF EXISTS config_approvals               CASCADE;
DROP TABLE IF EXISTS config_approval_votes          CASCADE;
DROP TABLE IF EXISTS config_dependencies            CASCADE;
DROP TABLE IF EXISTS config_notifications           CASCADE;
DROP TABLE IF EXISTS config_exports                 CASCADE;
DROP TABLE IF EXISTS config_cost_tracking           CASCADE;

-- Group 7: integration experiments
DROP TABLE IF EXISTS provider_configurations        CASCADE;
DROP TABLE IF EXISTS provider_specs                 CASCADE;
DROP TABLE IF EXISTS provider_data_mappings         CASCADE;
DROP TABLE IF EXISTS provider_metrics               CASCADE;
DROP TABLE IF EXISTS provider_sync_logs             CASCADE;
DROP TABLE IF EXISTS provider_webhooks              CASCADE;
DROP TABLE IF EXISTS integration_performance        CASCADE;
DROP TABLE IF EXISTS alert_bursts                   CASCADE;
DROP TABLE IF EXISTS alert_storms                   CASCADE;
DROP TABLE IF EXISTS alert_correlation_events       CASCADE;
DROP TABLE IF EXISTS alert_stream_events            CASCADE;
DROP TABLE IF EXISTS alert_metrics                  CASCADE;
DROP TABLE IF EXISTS event_stream_log               CASCADE;

-- Group 8: compliance/security
DROP TABLE IF EXISTS compliance_frameworks          CASCADE;
DROP TABLE IF EXISTS compliance_policies            CASCADE;
DROP TABLE IF EXISTS compliance_violations          CASCADE;
DROP TABLE IF EXISTS audit_configuration            CASCADE;
DROP TABLE IF EXISTS security_incidents             CASCADE;
DROP TABLE IF EXISTS vulnerability_findings         CASCADE;

-- Group 9: service catalog
DROP TABLE IF EXISTS services                       CASCADE;
DROP TABLE IF EXISTS service_catalog                CASCADE;
DROP TABLE IF EXISTS service_discovery_sources      CASCADE;
DROP TABLE IF EXISTS dependency_health              CASCADE;
DROP TABLE IF EXISTS k8s_service_accounts           CASCADE;
DROP TABLE IF EXISTS metric_exporters               CASCADE;
DROP TABLE IF EXISTS metric_retention_policies      CASCADE;

-- Group 10: misc duplicates
DROP TABLE IF EXISTS enhanced_correlation_rules     CASCADE;
DROP TABLE IF EXISTS rca_investigations             CASCADE;
DROP TABLE IF EXISTS oncall_schedule_entries        CASCADE;
DROP TABLE IF EXISTS workflow_templates             CASCADE;
DROP TABLE IF EXISTS workflow_schedules             CASCADE;
DROP TABLE IF EXISTS workflow_triggers              CASCADE;
DROP TABLE IF EXISTS workflow_permissions           CASCADE;
DROP TABLE IF EXISTS netapp_configs                 CASCADE;

-- Migration leftovers
DROP TABLE IF EXISTS alerts_old                     CASCADE;
DROP TABLE IF EXISTS audit_logs_old                 CASCADE;
DROP TABLE IF EXISTS notification_log_old           CASCADE;
DROP TABLE IF EXISTS incident_timeline_old          CASCADE;

COMMIT;
```
