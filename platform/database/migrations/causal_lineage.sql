-- =============================================================================
-- AlertHub: Causal Lineage & Inference Tables
-- Provides persistent storage for the CACIE engine output, cross-alert
-- causal relationship tracking, and known failure pattern templates.
-- All statements use IF NOT EXISTS / ON CONFLICT to be safe to re-run.
-- =============================================================================

BEGIN;

-- =============================================================================
-- causal_relationships
-- Persistent entity-to-entity causal link store. Each row records that
-- source_entity caused/affected target_entity within a specific incident.
-- The UNIQUE index lets us upsert (ON CONFLICT DO UPDATE) so observation_count
-- accumulates naturally rather than creating duplicate rows.
-- =============================================================================
CREATE TABLE IF NOT EXISTS causal_relationships (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_entity_id    TEXT        NOT NULL,
    target_entity_id    TEXT        NOT NULL,
    relationship_type   TEXT        NOT NULL
                        CHECK (relationship_type IN ('root_cause','downstream','sibling','correlated')),
    edge_type           TEXT        NOT NULL DEFAULT 'HOSTS',
    confidence_score    FLOAT       NOT NULL CHECK (confidence_score BETWEEN 0 AND 1),
    propagation_score   FLOAT                CHECK (propagation_score BETWEEN 0 AND 1),
    incident_id         UUID        REFERENCES incidents(id) ON DELETE SET NULL,
    alert_id            UUID        REFERENCES alerts(id) ON DELETE SET NULL,
    domain              TEXT,
    infra_level_source  INT,
    infra_level_target  INT,
    hop_index           INT         NOT NULL DEFAULT 0,
    first_seen_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_confirmed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    observation_count   INT         NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_cr_source    ON causal_relationships(source_entity_id);
CREATE INDEX IF NOT EXISTS idx_cr_target    ON causal_relationships(target_entity_id);
CREATE INDEX IF NOT EXISTS idx_cr_incident  ON causal_relationships(incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cr_type      ON causal_relationships(relationship_type);
CREATE INDEX IF NOT EXISTS idx_cr_domain    ON causal_relationships(domain) WHERE domain IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_cr_unique_edge
    ON causal_relationships(source_entity_id, target_entity_id, relationship_type, edge_type);

-- =============================================================================
-- propagation_paths
-- Records the full ordered causal chain (root → leaf) discovered during a
-- single incident's CACIE run. path_entities and path_edge_types are parallel
-- arrays of equal length (N entities, N-1 edges — store N for simplicity).
-- =============================================================================
CREATE TABLE IF NOT EXISTS propagation_paths (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id             UUID        REFERENCES incidents(id) ON DELETE CASCADE,
    root_entity_id          TEXT        NOT NULL,
    leaf_entity_id          TEXT        NOT NULL,
    path_entities           TEXT[]      NOT NULL,
    path_edge_types         TEXT[]      NOT NULL DEFAULT '{}',
    total_propagation_score FLOAT       NOT NULL,
    path_length             INT         NOT NULL,
    domain                  TEXT,
    computed_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pp_incident ON propagation_paths(incident_id);
CREATE INDEX IF NOT EXISTS idx_pp_root     ON propagation_paths(root_entity_id);
CREATE INDEX IF NOT EXISTS idx_pp_leaf     ON propagation_paths(leaf_entity_id);

-- =============================================================================
-- incident_causal_graphs
-- One row per incident: the complete causal snapshot produced by CACIE.
-- Stored in Postgres so it survives Redis TTL expiry (2h).
-- Replaces the per-column approach (rca_hypotheses + causal_chain) with a
-- single authoritative graph object, while keeping those columns for compat.
-- =============================================================================
CREATE TABLE IF NOT EXISTS incident_causal_graphs (
    incident_id         UUID        PRIMARY KEY REFERENCES incidents(id) ON DELETE CASCADE,
    root_entity_id      TEXT,
    root_entity_label   TEXT,
    root_entity_type    TEXT,
    root_infra_level    INT,
    root_confidence     FLOAT,
    domain              TEXT,
    blast_radius        JSONB       NOT NULL DEFAULT '[]',
    hypotheses          JSONB       NOT NULL DEFAULT '[]',
    causal_chain        JSONB       NOT NULL DEFAULT '[]',
    propagation_map     JSONB       NOT NULL DEFAULT '{}',
    suppressed_entities TEXT[]      NOT NULL DEFAULT '{}',
    reasoning           TEXT[]      NOT NULL DEFAULT '{}',
    engine_version      TEXT        NOT NULL DEFAULT 'v2',
    computed_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_icg_root_entity ON incident_causal_graphs(root_entity_id) WHERE root_entity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_icg_domain      ON incident_causal_graphs(domain)          WHERE domain IS NOT NULL;

-- =============================================================================
-- causal_pattern_templates
-- Knowledge base of known failure cascade patterns (seeded from prod data).
-- Used by CACIE to boost confidence when the incoming alert matches a known
-- template. observation_count and last_matched_at are updated automatically.
-- =============================================================================
CREATE TABLE IF NOT EXISTS causal_pattern_templates (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  TEXT        NOT NULL UNIQUE,
    description           TEXT,
    trigger_domain        TEXT        NOT NULL,
    trigger_entity_types  TEXT[]      NOT NULL,
    cascade_pattern       JSONB       NOT NULL,
    confidence_boost      FLOAT       NOT NULL DEFAULT 0.10
                          CHECK (confidence_boost BETWEEN 0 AND 0.50),
    observation_count     INT         NOT NULL DEFAULT 0,
    last_matched_at       TIMESTAMPTZ,
    is_active             BOOLEAN     NOT NULL DEFAULT true,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cpt_domain ON causal_pattern_templates(trigger_domain) WHERE is_active = true;

-- Seed well-known cascade patterns derived from prod alert analysis
INSERT INTO causal_pattern_templates
    (name, description, trigger_domain, trigger_entity_types, cascade_pattern, confidence_boost)
VALUES
  ('bm_node_pod_cascade',
   'Bare-metal failure → K8s node not ready → pod evictions',
   'compute',
   ARRAY['bare_metal','host'],
   '{"cascade":[{"from":"bare_metal","to":"k8s_node","edge":"HOSTS","weight":0.95},{"from":"k8s_node","to":"k8s_pod","edge":"RUNS_ON","weight":0.90}]}',
   0.15),

  ('kvm_vm_node_cascade',
   'KVM hypervisor overload → guest VM degradation → K8s node pressure',
   'compute',
   ARRAY['kvm','hypervisor'],
   '{"cascade":[{"from":"kvm","to":"cloudstack_vm","edge":"HOSTS","weight":0.92},{"from":"cloudstack_vm","to":"k8s_node","edge":"HOSTS","weight":0.90}]}',
   0.13),

  ('storage_iops_app_cascade',
   'NetApp/storage IOPS saturation → pod IO latency → application timeouts',
   'storage',
   ARRAY['netapp_aggregate','netapp_node','netapp_svm'],
   '{"cascade":[{"from":"netapp_aggregate","to":"k8s_pod","edge":"MOUNTS","weight":0.88},{"from":"k8s_pod","to":"cloud_application","edge":"RUNS_ON","weight":0.82}]}',
   0.12),

  ('network_saturation_cascade',
   'Network link saturation → host unreachable → K8s node pressure',
   'network',
   ARRAY['network_interface','host'],
   '{"cascade":[{"from":"network_interface","to":"host","edge":"MEMBER_OF","weight":0.85},{"from":"host","to":"k8s_node","edge":"HOSTS","weight":0.90}]}',
   0.10),

  ('db_connection_exhaustion_cascade',
   'Database connection pool exhaustion → service HTTP errors → alert storm',
   'database',
   ARRAY['postgresql','database','rds'],
   '{"cascade":[{"from":"database","to":"cloud_application","edge":"DEPENDS_ON","weight":0.88}]}',
   0.11),

  ('cluster_control_plane_cascade',
   'K8s control plane failure → all workloads degraded',
   'kubernetes',
   ARRAY['k8s_cluster','kubernetes_cluster'],
   '{"cascade":[{"from":"k8s_cluster","to":"k8s_node","edge":"MEMBER_OF","weight":0.95},{"from":"k8s_node","to":"k8s_pod","edge":"RUNS_ON","weight":0.92}]}',
   0.14)

ON CONFLICT (name) DO NOTHING;

-- =============================================================================
-- incidents: add recurrence_count column if missing
-- (used by the flapping-host reopen path in alert_pipeline.go)
-- =============================================================================
ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS recurrence_count INT NOT NULL DEFAULT 0;

-- =============================================================================
-- incidents: add source column if missing
-- (used by handleResolvedAlert title+source match)
-- =============================================================================
ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS source TEXT;

-- =============================================================================
-- incidents: add suppressed_entity_ids for blast-radius tracking
-- =============================================================================
ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS suppressed_entity_ids TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_incidents_suppressed
    ON incidents USING GIN(suppressed_entity_ids) WHERE array_length(suppressed_entity_ids,1) > 0;

-- =============================================================================
-- updated_at trigger for incident_causal_graphs
-- =============================================================================
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgname = 'trg_incident_causal_graphs_updated_at'
    ) THEN
        CREATE TRIGGER trg_incident_causal_graphs_updated_at
        BEFORE UPDATE ON incident_causal_graphs
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;

COMMIT;
