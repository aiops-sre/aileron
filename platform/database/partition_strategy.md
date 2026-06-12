# Partition Strategy — schema_v2

This document covers which tables are partitioned, why, how to maintain partitions over time, and how to archive or drop old data.

---

## Partitioned Tables

| Table                   | Partition Key  | Partition Type | Retention Policy |
|-------------------------|----------------|----------------|-----------------|
| `alerts`                | `created_at`   | RANGE monthly  | Keep 12 months hot, archive older |
| `alert_history`         | `created_at`   | RANGE monthly  | Keep 6 months hot, drop older |
| `correlation_results`   | `created_at`   | RANGE monthly  | Keep 6 months hot, archive older |
| `incident_timeline`     | `created_at`   | RANGE monthly  | Keep 12 months hot, archive older |
| `notification_log`      | `sent_at`      | RANGE monthly  | Keep 3 months hot, drop older |
| `audit_logs`            | `created_at`   | RANGE monthly  | Keep 24 months hot (compliance) |
| `service_health_checks` | `checked_at`   | RANGE monthly  | Keep 3 months hot, drop older |

---

## Why Range Partitioning by Month

**Query locality:** Dashboards query the last 24 hours to 7 days. With monthly partitions, PostgreSQL prunes all but 1–2 partitions from the query plan (partition pruning), reducing I/O by 99%+ on aged systems.

**Bulk deletes are instant:** Dropping a partition (`DROP TABLE alerts_2024_01`) is a metadata operation — it takes milliseconds and does not bloat the table or require `VACUUM`. Deleting old rows with `DELETE WHERE created_at < ...` on an unpartitioned table requires a full table scan and generates enormous WAL.

**Index sizes stay manageable:** Indexes on a monthly partition cover ~720k rows (assuming 1k alerts/hour). B-tree height stays at 3–4 levels. On an unpartitioned 36-month table at the same rate, a B-tree index would have 5–6 levels and require far more buffer pool space.

**Autovacuum efficiency:** Each partition is a separate heap. Autovacuum processes each independently, so a burst of updates/deletes in one month does not trigger full-table bloat in others.

---

## Pre-Created Partitions (2025–2027)

The `schema_v2.sql` and migration plan pre-create monthly partitions for all 7 tables from 2025-01 through 2027-12. This prevents partition creation from blocking inserts at month boundaries.

```sql
-- Verification query
SELECT
    parent.relname AS parent_table,
    child.relname  AS partition_name,
    pg_size_pretty(pg_relation_size(child.oid)) AS size
FROM pg_inherits
JOIN pg_class parent ON pg_inherits.inhparent = parent.oid
JOIN pg_class child  ON pg_inherits.inhrelid  = child.oid
WHERE parent.relname IN (
    'alerts','alert_history','correlation_results',
    'incident_timeline','notification_log','audit_logs','service_health_checks'
)
ORDER BY parent.relname, child.relname;
```

---

## Automated Partition Creation

The `ensure_next_month_partitions()` function in `schema_v2.sql` creates the next month's partitions if they don't exist. Run it as a cron job on the 25th of each month (5 days before month boundary to guarantee availability):

### Option A — PostgreSQL cron extension (pg_cron)

```sql
-- Requires pg_cron extension
CREATE EXTENSION IF NOT EXISTS pg_cron;

SELECT cron.schedule(
    'ensure-partitions',
    '0 2 25 * *',   -- 2am UTC on the 25th of every month
    'SELECT ensure_next_month_partitions()'
);
```

### Option B — Kubernetes CronJob (no pg_cron needed)

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: partition-maintenance
  namespace: aileron
spec:
  schedule: "0 2 25 * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
          - name: partition-maintenance
            image: postgres:14-alpine
            env:
            - name: PGPASSWORD
              valueFrom:
                secretKeyRef:
                  name: alerthub-secrets
                  key: postgres-password
            command:
            - psql
            - -h
            - postgres-primary.aileron.svc.cluster.local
            - -U
            - alerthub
            - -d
            - alerthub
            - -c
            - "SELECT ensure_next_month_partitions();"
```

Apply to the cluster:

```bash
kubectl config use-context example-cluster
kubectl apply -f k8s/partition-maintenance-cronjob.yaml -n aileron

# Verify it was created
kubectl get cronjob -n aileron

# Trigger a manual run to test
kubectl create job --from=cronjob/partition-maintenance partition-test \
  -n aileron
kubectl logs -n aileron -l job-name=partition-test --tail=20
```

### Option C — pg_partman (recommended for production)

`pg_partman` is the industry-standard partition management extension. It automates creation, retention, and archival.

```sql
CREATE EXTENSION IF NOT EXISTS pg_partman;

-- Register alerts table with pg_partman
SELECT partman.create_parent(
    p_parent_table  => 'public.alerts',
    p_control       => 'created_at',
    p_type          => 'native',
    p_interval      => 'monthly',
    p_premake       => 3    -- pre-create 3 future months
);

-- Set retention: keep 12 months, drop older
UPDATE partman.part_config
SET    retention            = '12 months',
       retention_keep_table = false,   -- actually drop, not just detach
       infinite_time_partitions = true
WHERE  parent_table = 'public.alerts';

-- Register all other partitioned tables similarly
SELECT partman.create_parent('public.alert_history',        'created_at', 'native', 'monthly', 3);
SELECT partman.create_parent('public.correlation_results',  'created_at', 'native', 'monthly', 3);
SELECT partman.create_parent('public.incident_timeline',    'created_at', 'native', 'monthly', 3);
SELECT partman.create_parent('public.notification_log',     'sent_at',    'native', 'monthly', 3);
SELECT partman.create_parent('public.audit_logs',           'created_at', 'native', 'monthly', 3);
SELECT partman.create_parent('public.service_health_checks','checked_at', 'native', 'monthly', 3);

-- Set individual retention policies
UPDATE partman.part_config SET retention = '6 months',  retention_keep_table = false WHERE parent_table = 'public.alert_history';
UPDATE partman.part_config SET retention = '6 months',  retention_keep_table = false WHERE parent_table = 'public.correlation_results';
UPDATE partman.part_config SET retention = '12 months', retention_keep_table = false WHERE parent_table = 'public.incident_timeline';
UPDATE partman.part_config SET retention = '3 months',  retention_keep_table = false WHERE parent_table = 'public.notification_log';
UPDATE partman.part_config SET retention = '24 months', retention_keep_table = false WHERE parent_table = 'public.audit_logs';
UPDATE partman.part_config SET retention = '3 months',  retention_keep_table = false WHERE parent_table = 'public.service_health_checks';
```

Then schedule pg_partman maintenance (every 4 hours is standard):

```sql
SELECT cron.schedule('partman-maintenance', '0 */4 * * *',
    'SELECT partman.run_maintenance_proc()');
```

---

## Retention Policies by Table

### `alerts` — 12 months hot

- Rationale: Incident post-mortems frequently reference alerts up to 6 months old. SLO calculations look back 30 days. 12 months covers all realistic queries.
- After 12 months: Archive to cold storage (see Archival section), then drop partition.

### `alert_history` — 6 months hot

- Rationale: Alert state timeline is used for compliance audits (90 days) and retrospectives (6 months is ample).
- After 6 months: Drop partition.

### `correlation_results` — 6 months hot

- Rationale: Correlation analytics rarely look back more than 90 days. 6 months provides a comfortable buffer.
- After 6 months: Archive (correlation stats are useful for model training), then drop.

### `incident_timeline` — 12 months hot

- Rationale: Timeline data is used in post-mortem documentation. Compliance and SRE teams reference it for up to a year.
- After 12 months: Archive, then drop.

### `notification_log` — 3 months hot

- Rationale: Notification delivery failures are acted on within days. 3 months is the maximum useful window.
- After 3 months: Drop partition.

### `audit_logs` — 24 months hot

- Rationale: Compliance requirements (SOC 2, internal security policy) typically mandate 12-month audit log retention. 24 months provides a compliance buffer.
- After 24 months: Archive to S3/object storage with immutable retention policy, then drop partition.

### `service_health_checks` — 3 months hot

- Rationale: Health check data is used for SLO calculations (30-day window). 3 months covers rolling calculations.
- After 3 months: Drop partition.

---

## Archival Strategy

For tables that require archival rather than deletion:

### Step 1 — Export to Parquet (preferred)

```bash
# Using pg2parquet or COPY TO
psql -U alerthub -d alerthub -c \
  "COPY (SELECT * FROM alerts_2024_06) TO STDOUT WITH CSV HEADER" \
  | gzip > s3://alerthub-archive/alerts/2024-06/alerts_2024_06.csv.gz
```

### Step 2 — Detach partition (keeps data accessible temporarily)

```sql
ALTER TABLE alerts DETACH PARTITION alerts_2024_06;
-- Table is now detached but still exists as a standalone table
-- Can still be queried directly for up to 30 days
```

### Step 3 — Drop after archival verification

```sql
-- Verify row count matches archive
SELECT COUNT(*) FROM alerts_2024_06;

-- Drop
DROP TABLE alerts_2024_06;
```

---

## Operational Queries

### Check partition sizes

```sql
SELECT
    child.relname           AS partition,
    pg_size_pretty(pg_relation_size(child.oid)) AS data_size,
    pg_size_pretty(pg_total_relation_size(child.oid)) AS total_size
FROM pg_inherits
JOIN pg_class parent ON pg_inherits.inhparent = parent.oid
JOIN pg_class child  ON pg_inherits.inhrelid  = child.oid
WHERE parent.relname = 'alerts'
ORDER BY child.relname;
```

### Check partition pruning is working

```sql
EXPLAIN (ANALYZE, COSTS OFF)
SELECT * FROM alerts
WHERE created_at BETWEEN NOW() - INTERVAL '7 days' AND NOW()
  AND status = 'open';
-- Should show: Partitions selected: 1 or 2 (not all)
```

### Find missing future partitions

```sql
-- Shows months in next 3 months that don't have a partition
WITH months AS (
    SELECT generate_series(
        date_trunc('month', NOW()),
        date_trunc('month', NOW() + INTERVAL '3 months'),
        '1 month'
    )::date AS month_start
),
existing AS (
    SELECT child.relname
    FROM pg_inherits
    JOIN pg_class parent ON pg_inherits.inhparent = parent.oid
    JOIN pg_class child  ON pg_inherits.inhrelid  = child.oid
    WHERE parent.relname = 'alerts'
)
SELECT
    month_start,
    'alerts_' || TO_CHAR(month_start, 'YYYY_MM') AS expected_partition,
    EXISTS (
        SELECT 1 FROM existing
        WHERE relname = 'alerts_' || TO_CHAR(month_start, 'YYYY_MM')
    ) AS exists
FROM months;
```

### Manual partition creation (emergency)

```sql
SELECT create_monthly_partition('alerts', '2028-01-01'::date);
SELECT create_monthly_partition('correlation_results', '2028-01-01'::date);
```

---

## Alert for Upcoming Partition Gaps

Add this as a Prometheus alert rule or a weekly database health check:

```sql
-- Returns rows if any partitioned table is missing its next-month partition
SELECT
    t.tbl AS table_name,
    'alerts_' || TO_CHAR(DATE_TRUNC('month', NOW() + INTERVAL '1 month'), 'YYYY_MM')
        AS next_partition,
    NOT EXISTS (
        SELECT 1
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = t.tbl || '_' ||
              TO_CHAR(DATE_TRUNC('month', NOW() + INTERVAL '1 month'), 'YYYY_MM')
          AND n.nspname = 'public'
    ) AS missing
FROM (
    VALUES
        ('alerts'), ('alert_history'), ('correlation_results'),
        ('incident_timeline'), ('notification_log'),
        ('audit_logs'), ('service_health_checks')
) AS t(tbl)
WHERE NOT EXISTS (
    SELECT 1
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relname = t.tbl || '_' ||
          TO_CHAR(DATE_TRUNC('month', NOW() + INTERVAL '1 month'), 'YYYY_MM')
      AND n.nspname = 'public'
);
-- Zero rows = healthy. Any row = alert.
```

---

## What Is NOT Partitioned (and Why)

| Table                  | Why Not Partitioned                                              |
|------------------------|------------------------------------------------------------------|
| `users`                | Low volume, high-cardinality lookups by email/ID — partitioning adds overhead |
| `incidents`            | Medium volume, incident queries span time ranges but include cross-period joins |
| `correlation_clusters` | Small table, acts as a header; results are in partitioned correlation_results |
| `workflows`            | Configuration data, not time-series                              |
| `integrations`         | Configuration data                                               |
| `notification_channels`| Configuration data                                               |
| `roles`, `permissions` | Static lookup tables                                             |
| `oncall_shifts`        | Low write volume; partial index on future shifts is sufficient   |
