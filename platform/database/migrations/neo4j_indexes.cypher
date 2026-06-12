// =============================================================================
// AlertHub Neo4j Indexes for CACIE / RecursiveTopoRCAEngine
//
// Apply once to the Neo4j instance via cypher-shell or the Neo4j browser:
//   cypher-shell -u neo4j -p <pass> < neo4j_indexes.cypher
//
// All statements are idempotent (IF NOT EXISTS).
// =============================================================================

// --- Entity property indexes (required by upward/downward sweep Cyphers) ---

CREATE INDEX entity_id_idx IF NOT EXISTS
FOR (n:Entity) ON (n.entity_id);

CREATE INDEX entity_type_idx IF NOT EXISTS
FOR (n:Entity) ON (n.entity_type);

CREATE INDEX entity_infra_level_idx IF NOT EXISTS
FOR (n:Entity) ON (n.infra_level);

CREATE INDEX entity_cluster_idx IF NOT EXISTS
FOR (n:Entity) ON (n.cluster);

CREATE INDEX entity_namespace_idx IF NOT EXISTS
FOR (n:Entity) ON (n.namespace);

CREATE INDEX entity_label_idx IF NOT EXISTS
FOR (n:Entity) ON (n.label);

// --- Composite index for the most common filter pattern: cluster + type ---
CREATE INDEX entity_cluster_type_idx IF NOT EXISTS
FOR (n:Entity) ON (n.cluster, n.entity_type);

// --- Relationship property index for weighted traversal scoring ---
CREATE INDEX rel_weight_idx IF NOT EXISTS
FOR ()-[r:HOSTS]-() ON (r.weight);

CREATE INDEX rel_runs_on_weight_idx IF NOT EXISTS
FOR ()-[r:RUNS_ON]-() ON (r.weight);

CREATE INDEX rel_mounts_weight_idx IF NOT EXISTS
FOR ()-[r:MOUNTS]-() ON (r.weight);
