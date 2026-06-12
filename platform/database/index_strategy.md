# Index Strategy — schema_v2

This document explains every index in `schema_v2.sql`: what query pattern it supports, why it was chosen over alternatives, and what it costs. Use this to tune, add, or remove indexes as usage patterns evolve.

---

## Guiding Principles

1. **Index the query, not the table.** Every index here exists because a specific, measurable query pattern requires it.
2. **Partial indexes over full indexes.** When a query always filters on a status/flag, the partial index is smaller, fits in memory, and is updated less frequently.
3. **Composite index column order = selectivity order.** The most selective column goes first for range scans; the equality column goes first for `=` lookups followed by ORDER BY.
4. **GIN for arrays and JSONB.** B-tree cannot efficiently search inside arrays or JSON documents.
5. **Trigram (pg_trgm) for title search.** `ILIKE '%keyword%'` with a trigram GIN index is orders of magnitude faster than a sequential scan.
6. **BRIN for time-series data.** Block Range INdex is tiny (hundreds of bytes vs MB for B-tree) and effective on naturally-ordered append-only columns like `created_at` in partitioned tables, where each partition's physical blocks correlate perfectly with time.

---

## Platform Domain

### `roles`

```sql
idx_roles_name ON roles(name)
```
- **Query:** `SELECT * FROM roles WHERE name = 'admin'`
- **Why B-tree:** Low-cardinality exact lookup. Roles table is tiny so this index is mainly for FK validation speed.

### `permissions`

```sql
idx_permissions_resource_action ON permissions(resource, action)
```
- **Query:** `SELECT * FROM permissions WHERE resource = 'alerts' AND action = 'write'`
- **Why composite:** The RBAC check always filters on both columns together. Single-column indexes would require a merge.

### `teams`

```sql
idx_teams_name        ON teams(name)
idx_teams_parent      ON teams(parent_team_id) WHERE parent_team_id IS NOT NULL
idx_teams_manager     ON teams(manager_id)     WHERE manager_id IS NOT NULL
idx_teams_active      ON teams(is_active)      WHERE is_active = true
```
- `idx_teams_parent`: supports `SELECT * FROM teams WHERE parent_team_id = $1` (team hierarchy traversal). Partial because root teams have NULL parent.
- `idx_teams_manager`: supports assignment queries. Partial because manager is optional.

### `users`

```sql
idx_users_email    ON users(email)          -- login by email
idx_users_username ON users(username)       -- login by username, @mention
idx_users_role_id  ON users(role_id)        -- RBAC queries: all users with role X
idx_users_team_id  ON users(team_id) WHERE team_id IS NOT NULL   -- team membership
idx_users_active   ON users(is_active) WHERE is_active = true    -- filter inactive
idx_users_oauth    ON users(oauth_source, oauth_subject)          -- SSO upsert
```
- `idx_users_oauth`: The MAS/OIDC login flow does `WHERE oauth_source = 'mas' AND oauth_subject = $dsid`. Without this index, every SSO login would scan the entire users table.

### `user_sessions`

```sql
idx_user_sessions_user_id ON user_sessions(user_id)
idx_user_sessions_token   ON user_sessions(token_hash)
idx_user_sessions_expires ON user_sessions(expires_at)
idx_user_sessions_active  ON user_sessions(user_id, expires_at)
    WHERE revoked = false AND expires_at > NOW()
```
- `idx_user_sessions_token`: Every authenticated API request validates the Bearer token hash. This must be a primary lookup index.
- `idx_user_sessions_active`: The session cleanup job runs `DELETE FROM user_sessions WHERE expires_at < NOW()`. The partial index on non-revoked, non-expired sessions supports the active-session query in auth middleware.

---

## Alerts Domain

### `alerts` (partitioned)

The alerts table is the hottest table in the system. Index design is critical.

```sql
idx_alerts_status_sev_created ON alerts(status, severity, created_at DESC)
```
- **Primary query:** Dashboard alert list — `WHERE status = 'open' ORDER BY severity, created_at DESC LIMIT 50`
- **Why this order:** `status` is the most selective equality filter (99% of alerts are open/ack/resolved — open is ~60%). Severity orders within status. `created_at DESC` matches the ORDER BY direction exactly, so PostgreSQL can avoid a sort.

```sql
idx_alerts_source_sourceid ON alerts(source, source_id)
```
- **Query:** Deduplication on ingest — `WHERE source = $1 AND source_id = $2`
- **Why composite:** Source alone is not selective. The combination is nearly unique.

```sql
idx_alerts_fingerprint ON alerts(fingerprint) WHERE fingerprint IS NOT NULL
```
- **Query:** `WHERE fingerprint = $1` — content-based dedup
- **Why partial:** ~30% of alerts have no fingerprint. Excluding them halves the index size.

```sql
idx_alerts_assigned_to ON alerts(assigned_to) WHERE assigned_to IS NOT NULL
```
- **Query:** "My alerts" — `WHERE assigned_to = $user_id AND status != 'resolved'`
- **Why partial:** Unassigned alerts (NULL) are never queried by this filter.

```sql
idx_alerts_team_id ON alerts(team_id) WHERE team_id IS NOT NULL
```
- **Query:** Team dashboard — `WHERE team_id = $1 AND status != 'closed'`

```sql
idx_alerts_incident_id ON alerts(incident_id) WHERE incident_id IS NOT NULL
```
- **Query:** `SELECT * FROM alerts WHERE incident_id = $1` — incident detail page loads all associated alerts.

```sql
idx_alerts_correlation_id ON alerts(correlation_id) WHERE correlation_id IS NOT NULL
```
- **Query:** Correlation result page — fetch all alerts in a group.

```sql
idx_alerts_entity ON alerts(entity_type, entity_id)
```
- **Query:** Topology-driven alert lookup — `WHERE entity_type = 'service' AND entity_id = 'api-gateway'`
- **Why composite:** Entity type alone is not selective. Together they identify a specific resource.

```sql
idx_alerts_region ON alerts(region) WHERE region IS NOT NULL
```
- **Query:** Region filter on dashboard.

```sql
idx_alerts_tags ON alerts USING GIN(tags)
```
- **Query:** `WHERE tags @> ARRAY['k8s','production']` — multi-tag filter.
- **Why GIN:** Tags is a TEXT array. B-tree cannot index array containment.

```sql
idx_alerts_labels ON alerts USING GIN(labels jsonb_path_ops)
```
- **Query:** `WHERE labels @> '{"env":"prod","team":"sre"}'` — label selector.
- **Why jsonb_path_ops:** Optimized for containment (`@>`) queries. Smaller than default `jsonb_ops`.

```sql
idx_alerts_active ON alerts(created_at DESC, severity)
    WHERE status IN ('open','acknowledged')
```
- **Query:** Active alert count, SLO burn rate calculation — only open/ack alerts.
- **Why partial composite:** Filters out resolved/closed alerts (majority after system runs for weeks). The covering `created_at DESC, severity` supports time-range aggregations with no extra sort.

```sql
idx_alerts_title_trgm ON alerts USING GIN(title gin_trgm_ops)
```
- **Query:** Full-text search — `WHERE title ILIKE '%database connection%'`
- **Why trigram GIN:** `LIKE '%word%'` cannot use B-tree. Trigram GIN decomposes text into 3-char grams and finds matches in O(n_grams) instead of O(rows).
- **Requires:** `CREATE EXTENSION pg_trgm`

### `alert_history` (partitioned)

```sql
idx_alert_history_alert_id ON alert_history(alert_id, created_at DESC)
```
- **Query:** Alert timeline — `WHERE alert_id = $1 ORDER BY created_at DESC`
- **Why composite with DESC:** The ORDER BY direction is baked into the index, so no extra sort. `alert_id` first because equality always precedes the range scan.

### `alert_comments`

```sql
idx_alert_comments_alert_id   ON alert_comments(alert_id)
idx_alert_comments_created_at ON alert_comments(created_at DESC)
```

### `maintenance_windows`

```sql
idx_mw_alert_id ON maintenance_windows(alert_id)  WHERE alert_id IS NOT NULL
idx_mw_active   ON maintenance_windows(start_time, end_time)
    WHERE end_time IS NULL OR end_time > NOW()
```
- `idx_mw_active`: The pipeline checks maintenance windows on every ingest. The partial index contains only active windows (a tiny fraction of all windows), making the check nearly free.

### `alert_assignments`

```sql
idx_alert_assignments_alert  ON alert_assignments(alert_id)
idx_alert_assignments_user   ON alert_assignments(user_id)   WHERE user_id IS NOT NULL
idx_alert_assignments_team   ON alert_assignments(team_id)   WHERE team_id IS NOT NULL
idx_alert_assignments_active ON alert_assignments(alert_id)  WHERE unassigned_at IS NULL
```
- `idx_alert_assignments_active`: "Current assignee" query only cares about unrevoked assignments. The partial index is tiny.

---

## Incidents Domain

### `incidents`

```sql
idx_incidents_status_sev   ON incidents(status, severity, created_at DESC)
```
- Same composite rationale as alerts: dashboard primary query.

```sql
idx_incidents_assigned     ON incidents(assigned_to)   WHERE assigned_to IS NOT NULL
idx_incidents_team         ON incidents(team_id)        WHERE team_id IS NOT NULL
idx_incidents_number       ON incidents(incident_number)
```
- `idx_incidents_number`: Incident numbers are human-readable (`INC-1042`). Lookup by number is a common support URL pattern.

```sql
idx_incidents_correlation  ON incidents(correlation_id) WHERE correlation_id IS NOT NULL
idx_incidents_created_at   ON incidents(created_at DESC)
idx_incidents_alert_ids    ON incidents USING GIN(alert_ids)
```
- `idx_incidents_alert_ids`: `WHERE alert_ids @> ARRAY[$alert_id::UUID]` — find the incident containing a given alert. GIN on UUID array.

```sql
idx_incidents_active ON incidents(created_at DESC, severity)
    WHERE status NOT IN ('resolved','closed')
```
- Active incident count is a first-class metric on the dashboard.

### `incident_timeline` (partitioned)

```sql
idx_incident_timeline_incident ON incident_timeline(incident_id, created_at DESC)
```
- Same reasoning as alert_history.

---

## Correlation Domain

### `correlation_results` (partitioned)

```sql
idx_corr_results_alert_id ON correlation_results(alert_id, created_at DESC)
```
- **Primary lookup:** Given an alert, find all correlation results across all strategies.
- **Why not just alert_id:** Including `created_at DESC` means PostgreSQL can answer "latest correlation for alert X" with an index-only scan on each partition.

```sql
idx_corr_results_correlation_id ON correlation_results(correlation_id)
    WHERE correlation_id IS NOT NULL
```
- **Query:** Fetch all alerts in a correlation group by group ID.

```sql
idx_corr_results_type ON correlation_results(correlation_type, created_at DESC)
```
- **Query:** Analytics — "how many AI correlations ran last week?"

```sql
idx_corr_results_duplicate ON correlation_results(duplicate_of)
    WHERE is_duplicate = true AND duplicate_of IS NOT NULL
```
- **Query:** Deduplication report — find all aliases of a canonical alert.
- **Why partial:** Only duplicate alerts have `duplicate_of` set. This index is tiny.

```sql
idx_corr_results_incident ON correlation_results(incident_id)
    WHERE incident_id IS NOT NULL
```
- **Query:** Incident correlation evidence page.

### `correlation_clusters`

```sql
idx_corr_clusters_key      ON correlation_clusters(cluster_key)
idx_corr_clusters_status   ON correlation_clusters(status) WHERE status = 'active'
idx_corr_clusters_incident ON correlation_clusters(incident_id) WHERE incident_id IS NOT NULL
```

### `correlation_rules`

```sql
idx_corr_rules_enabled  ON correlation_rules(enabled)   WHERE enabled = true
idx_corr_rules_type     ON correlation_rules(rule_type)
idx_corr_rules_priority ON correlation_rules(priority DESC) WHERE enabled = true
```
- `idx_corr_rules_priority`: The engine loads active rules ordered by priority on startup. This index makes that load instant.

### `alert_similarity_cache`

```sql
idx_similarity_pair    ON alert_similarity_cache(alert_id_a, alert_id_b)
idx_similarity_expires ON alert_similarity_cache(expires_at)
```
- `idx_similarity_expires`: Cache eviction job deletes expired entries. Without this index, the DELETE would scan the entire table.

---

## Topology Domain

### `topology_entities`

```sql
idx_topo_entities_source ON topology_entities(source, entity_type)
```
- **Query:** `WHERE source = 'dynatrace' AND entity_type = 'service'` — bulk topology load by source.

```sql
idx_topo_entities_extid ON topology_entities(external_id)
```
- **Query:** Upsert on sync — `ON CONFLICT (source, external_id)`

```sql
idx_topo_entities_zone    ON topology_entities(management_zone) WHERE management_zone IS NOT NULL
idx_topo_entities_cluster ON topology_entities(cluster_name)    WHERE cluster_name IS NOT NULL
idx_topo_entities_labels  ON topology_entities USING GIN(labels)
```
- `idx_topo_entities_labels`: `WHERE labels @> '{"k8s_namespace":"default"}'`

### `topology_sync_log`

```sql
idx_topo_sync_source ON topology_sync_log(source_id, created_at DESC)
idx_topo_sync_status ON topology_sync_log(status, created_at DESC)
```

---

## Config Domain

### `k8s_cluster_configs`

```sql
idx_k8s_configs_enabled ON k8s_cluster_configs(enabled)     WHERE enabled = true
idx_k8s_configs_env     ON k8s_cluster_configs(environment)
idx_k8s_configs_region  ON k8s_cluster_configs(region)
```

### `alert_sources`

```sql
idx_alert_sources_type    ON alert_sources(source_type)
idx_alert_sources_enabled ON alert_sources(enabled) WHERE enabled = true
```

### `integrations`

```sql
idx_integrations_type   ON integrations(type)
idx_integrations_active ON integrations(is_active) WHERE is_active = true
```

### `llm_feedback`

```sql
idx_llm_feedback_alert ON llm_feedback(alert_id) WHERE alert_id IS NOT NULL
```

---

## Notifications Domain

### `notification_log` (partitioned)

```sql
idx_notif_log_alert    ON notification_log(alert_id)   WHERE alert_id IS NOT NULL
idx_notif_log_incident ON notification_log(incident_id) WHERE incident_id IS NOT NULL
idx_notif_log_sent_at  ON notification_log(sent_at DESC)
```
- All partial indexes because many notifications are not tied to a specific alert or incident.

### `notification_rules`

```sql
idx_notif_rules_active ON notification_rules(is_active, priority DESC)
    WHERE is_active = true
```
- The notification engine loads active rules ordered by priority on every ingest event.

---

## Workflows Domain

### `workflow_executions`

```sql
idx_workflow_exec_workflow ON workflow_executions(workflow_id, started_at DESC)
idx_workflow_exec_status   ON workflow_executions(status, started_at DESC)
```
- `idx_workflow_exec_status`: Finds stuck `running` executions for timeout cleanup.

### `runbook_executions`

```sql
idx_runbook_exec_runbook  ON runbook_executions(runbook_id, started_at DESC)
idx_runbook_exec_alert    ON runbook_executions(alert_id)   WHERE alert_id IS NOT NULL
idx_runbook_exec_incident ON runbook_executions(incident_id) WHERE incident_id IS NOT NULL
```

---

## Platform Audit

### `audit_logs` (partitioned)

```sql
idx_audit_user     ON audit_logs(user_id, created_at DESC)
idx_audit_action   ON audit_logs(action, created_at DESC)
idx_audit_resource ON audit_logs(resource_type, resource_id, created_at DESC)
idx_audit_created  ON audit_logs(created_at DESC)
```
- `idx_audit_resource`: Compliance query — "all actions on alert <id> in the last 30 days". Composite `(resource_type, resource_id, created_at)` answers this with an index-only scan.

### `security_events`

```sql
idx_sec_events_type      ON security_events(event_type, created_at DESC)
idx_sec_events_unresolved ON security_events(created_at DESC) WHERE resolved = false
```
- `idx_sec_events_unresolved`: Security dashboard shows only unresolved events. The partial index contains a tiny fraction of all events.

---

## On-Call Domain

### `oncall_shifts`

```sql
idx_oncall_shifts_schedule ON oncall_shifts(schedule_id)
idx_oncall_shifts_user     ON oncall_shifts(user_id)
idx_oncall_shifts_time     ON oncall_shifts(start_time, end_time)
idx_oncall_shifts_current  ON oncall_shifts(start_time, end_time)
    WHERE end_time > NOW()
```
- `idx_oncall_shifts_current`: "Who is on call right now?" query — `WHERE start_time <= NOW() AND end_time > NOW()`. The partial index discards all past shifts.

---

## What Was NOT Indexed (and Why)

| Column                          | Why Not Indexed                                              |
|---------------------------------|--------------------------------------------------------------|
| `alerts.description`            | Free-text search belongs in Weaviate, not PostgreSQL         |
| `alerts.metadata` (full)        | JSONB full-document index is expensive; specific keys use GIN containment |
| `incidents.title`               | Incident titles are searched via API params that use the trigram index on alerts |
| `correlation_results.metadata`  | Metadata is write-heavy; any reads use containment via GIN on specific tables |
| `users.password_hash`           | Never queried by value (always looked up by email/username first) |
| `notification_log.body`         | Notification bodies are never searched                       |
| `audit_logs.old_value/new_value`| JSONB audit values are displayed, never filtered             |

---

## Index Maintenance

```sql
-- Check index bloat monthly
SELECT
    schemaname,
    tablename,
    indexname,
    pg_size_pretty(pg_relation_size(indexrelid)) AS index_size,
    idx_scan,
    idx_tup_read,
    idx_tup_fetch
FROM pg_stat_user_indexes
ORDER BY pg_relation_size(indexrelid) DESC
LIMIT 30;

-- Find unused indexes (idx_scan = 0 for > 7 days)
SELECT schemaname, tablename, indexname, idx_scan
FROM pg_stat_user_indexes
WHERE idx_scan = 0
  AND indexrelid NOT IN (SELECT conindid FROM pg_constraint)
ORDER BY pg_relation_size(indexrelid) DESC;

-- Rebuild bloated indexes
REINDEX INDEX CONCURRENTLY idx_alerts_status_sev_created;
```
