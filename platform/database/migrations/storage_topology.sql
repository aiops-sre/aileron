-- storage_topology.sql
-- Adds storage-layer topology support:
--   1. Additional columns on topology_entities for storage-specific metadata
--   2. storage_volume_mappings: pvc→pv→netapp_volume relationship table
--   3. netapp_volumes: raw NetApp volume inventory ingested from ONTAP API
--   4. netapp_aggregates: raw NetApp aggregate (storage pool) inventory

-- ── topology_entities: storage metadata columns ───────────────────────────────
ALTER TABLE topology_entities
    ADD COLUMN IF NOT EXISTS provisioner           VARCHAR(255),
    ADD COLUMN IF NOT EXISTS backend_storage_id    VARCHAR(255),
    ADD COLUMN IF NOT EXISTS backend_volume_name   VARCHAR(255),
    ADD COLUMN IF NOT EXISTS capacity_bytes        BIGINT,
    ADD COLUMN IF NOT EXISTS allocated_bytes       BIGINT,
    ADD COLUMN IF NOT EXISTS storage_pool          VARCHAR(255),
    ADD COLUMN IF NOT EXISTS nfs_server            VARCHAR(255),
    ADD COLUMN IF NOT EXISTS nfs_path              VARCHAR(255),
    ADD COLUMN IF NOT EXISTS utilization_pct       FLOAT;

CREATE INDEX IF NOT EXISTS idx_te_provisioner         ON topology_entities (provisioner);
CREATE INDEX IF NOT EXISTS idx_te_backend_storage_id  ON topology_entities (backend_storage_id);

-- ── storage_volume_mappings: explicit pvc→pv→backend edges ───────────────────
CREATE TABLE IF NOT EXISTS storage_volume_mappings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id       UUID NOT NULL REFERENCES topology_entities(id) ON DELETE CASCADE,
    target_id       UUID NOT NULL REFERENCES topology_entities(id) ON DELETE CASCADE,
    mapping_type    VARCHAR(50) NOT NULL,  -- pvc_to_pv | pv_to_netapp_volume | pv_to_netapp_aggregate
    cluster_name    VARCHAR(255),
    namespace_name  VARCHAR(255),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (source_id, target_id, mapping_type)
);

CREATE INDEX IF NOT EXISTS idx_svm_source  ON storage_volume_mappings (source_id);
CREATE INDEX IF NOT EXISTS idx_svm_target  ON storage_volume_mappings (target_id);

-- ── netapp_aggregates: ONTAP storage pool inventory ──────────────────────────
CREATE TABLE IF NOT EXISTS netapp_aggregates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_name    VARCHAR(255) NOT NULL,  -- NetApp cluster hostname/name
    aggregate_name  VARCHAR(255) NOT NULL,
    node_name       VARCHAR(255),           -- ONTAP controller node that owns this aggregate
    state           VARCHAR(50),            -- online/offline/restricted
    capacity_bytes  BIGINT,
    used_bytes      BIGINT,
    available_bytes BIGINT,
    utilization_pct FLOAT GENERATED ALWAYS AS (
        CASE WHEN capacity_bytes > 0
             THEN ROUND((used_bytes::FLOAT / capacity_bytes::FLOAT * 100)::NUMERIC, 2)::FLOAT
             ELSE 0 END
    ) STORED,
    raid_type       VARCHAR(50),
    disk_count      INT,
    raw_data        JSONB,
    last_synced_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (cluster_name, aggregate_name)
);

CREATE INDEX IF NOT EXISTS idx_na_cluster  ON netapp_aggregates (cluster_name);
CREATE INDEX IF NOT EXISTS idx_na_util     ON netapp_aggregates (utilization_pct DESC);

-- ── netapp_volumes: ONTAP volume inventory ────────────────────────────────────
CREATE TABLE IF NOT EXISTS netapp_volumes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_name        VARCHAR(255) NOT NULL,
    volume_name         VARCHAR(255) NOT NULL,
    svm_name            VARCHAR(255),         -- Storage Virtual Machine (SVM/Vserver)
    aggregate_name      VARCHAR(255),
    state               VARCHAR(50),          -- online/offline/restricted
    volume_type         VARCHAR(50),          -- rw/dp/ls
    capacity_bytes      BIGINT,
    used_bytes          BIGINT,
    available_bytes     BIGINT,
    utilization_pct     FLOAT GENERATED ALWAYS AS (
        CASE WHEN capacity_bytes > 0
             THEN ROUND((used_bytes::FLOAT / capacity_bytes::FLOAT * 100)::NUMERIC, 2)::FLOAT
             ELSE 0 END
    ) STORED,
    junction_path       VARCHAR(512),         -- NFS export mount path
    export_policy       VARCHAR(255),
    security_style      VARCHAR(50),          -- unix/ntfs/mixed
    nfs_server          VARCHAR(255),
    trident_backend_uuid VARCHAR(255),        -- links to K8s PV annotation
    k8s_pv_name         VARCHAR(255),        -- linked K8s PersistentVolume name
    raw_data            JSONB,
    last_synced_at      TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (cluster_name, svm_name, volume_name)
);

CREATE INDEX IF NOT EXISTS idx_nv_cluster        ON netapp_volumes (cluster_name);
CREATE INDEX IF NOT EXISTS idx_nv_aggregate      ON netapp_volumes (aggregate_name);
CREATE INDEX IF NOT EXISTS idx_nv_junction_path  ON netapp_volumes (junction_path);
CREATE INDEX IF NOT EXISTS idx_nv_trident_uuid   ON netapp_volumes (trident_backend_uuid);
CREATE INDEX IF NOT EXISTS idx_nv_util           ON netapp_volumes (utilization_pct DESC);

-- ── topology_sources: ensure netapp type is accepted ─────────────────────────
-- (already in schema_v2.sql but re-stated here for migration idempotency)
INSERT INTO topology_sources (source_type, display_name, sync_enabled, sync_interval_mins)
VALUES ('netapp', 'NetApp ONTAP', false, 15)
ON CONFLICT DO NOTHING;
