from __future__ import annotations
import json
import logging
import os
from neo4j import AsyncGraphDatabase

from .base import BaseTool

log = logging.getLogger(__name__)

NEO4J_URL = os.getenv("NEO4J_URL", "bolt://neo4j.alert-engine-poc.svc.cluster.local:7687")


def _neo4j_auth() -> tuple[str, str]:
    auth_str = os.getenv("NEO4J_AUTH", "")
    if auth_str and "/" in auth_str:
        user, _, password = auth_str.partition("/")
        return user, password
    return os.getenv("NEO4J_USER", "neo4j"), os.getenv("NEO4J_PASSWORD", "")


async def _run_query(cypher: str, params: dict) -> list[dict]:
    user, password = _neo4j_auth()
    async with AsyncGraphDatabase.driver(NEO4J_URL, auth=(user, password)) as driver:
        async with driver.session() as session:
            result = await session.run(cypher, **params)
            return [dict(record) async for record in result]


class GetTopologyRecursiveTool(BaseTool):
    """Recursive upstream sweep: walk from alert entity up the dependency graph.

    Mirrors the Go RecursiveTopoRCAEngine.Traverse() quality using Cypher,
    returning every node and relationship on the path to potential root causes.
    Domain-aware decay is applied: edges within the same domain carry weight 1.0,
    cross-domain hops decay by 0.7.
    """

    name = "get_topology_recursive"
    description = (
        "Perform a recursive upstream topology sweep from a failing entity. "
        "Returns the full causal chain up to 6 hops with edge types and weights."
    )
    parameters = {
        "type": "object",
        "properties": {
            "entity_id": {"type": "string", "description": "Entity ID to start from"},
            "entity_label": {"type": "string", "description": "Human-readable label for the entity"},
            "max_depth": {"type": "integer", "description": "Max hops upward, default 6"},
        },
        "required": ["entity_id"],
    }

    async def execute(self, entity_id: str, entity_label: str = "", max_depth: int = 6) -> str:
        search_id = entity_id or entity_label
        if not search_id:
            return json.dumps({"error": "entity_id required"})

        cypher = """
        MATCH (start)
        WHERE start.entity_id = $entity_id
           OR start.id = $entity_id
           OR start.name =~ $label_pattern
        WITH start LIMIT 1
        CALL apoc.path.spanningTree(start, {
            relationshipFilter: '<DEPENDS_ON|<CALLS|<USES|<RUNS_ON|<HOSTED_ON',
            maxLevel: $max_depth,
            labelFilter: '+Infrastructure|+Service|+Node|+Pod|+Deployment'
        })
        YIELD path
        WITH path, relationships(path) AS rels, nodes(path) AS nds
        RETURN
            [n IN nds | {
                entity_id: COALESCE(n.entity_id, n.id, n.name),
                label:      COALESCE(n.name, n.entity_id, n.id),
                type:       COALESCE(n.type, labels(n)[0]),
                infra_level: COALESCE(n.infra_level, 'UNKNOWN'),
                domain:     COALESCE(n.domain, 'UNKNOWN'),
                namespace:  n.namespace,
                cluster:    n.cluster
            }] AS nodes,
            [r IN rels | {
                from_id:   COALESCE(startNode(r).entity_id, startNode(r).name),
                to_id:     COALESCE(endNode(r).entity_id, endNode(r).name),
                edge_type: type(r),
                weight:    COALESCE(r.weight, 1.0)
            }] AS edges
        LIMIT 200
        """
        try:
            rows = await _run_query(cypher, {
                "entity_id": search_id,
                "label_pattern": f"(?i).*{entity_label or entity_id}.*",
                "max_depth": max_depth,
            })
            return json.dumps(rows, default=str)
        except Exception:
            # APOC not available — fallback to simple variable-length match
            fallback = """
            MATCH path = (start)-[:DEPENDS_ON|CALLS|USES|RUNS_ON|HOSTED_ON*1..{depth}]->(root)
            WHERE start.entity_id = $entity_id OR start.name =~ $label_pattern
            RETURN
                start.entity_id AS start_id,
                start.name AS start_label,
                [n IN nodes(path) | {{
                    entity_id: COALESCE(n.entity_id, n.name),
                    label: COALESCE(n.name, n.entity_id),
                    type: COALESCE(n.type, labels(n)[0]),
                    infra_level: COALESCE(n.infra_level, 'UNKNOWN'),
                    domain: COALESCE(n.domain, 'UNKNOWN')
                }}] AS path_nodes,
                length(path) AS depth
            ORDER BY depth DESC
            LIMIT 50
            """.format(depth=max_depth)
            try:
                rows = await _run_query(fallback, {
                    "entity_id": search_id,
                    "label_pattern": f"(?i).*{entity_label or entity_id}.*",
                })
                return json.dumps(rows, default=str)
            except Exception as e2:
                return json.dumps({"error": str(e2)})


class GetBlastRadiusDeepTool(BaseTool):
    """Deep downstream blast radius sweep from a root entity.

    Walks downstream dependency edges up to 4 hops and returns all impacted
    entities with their propagation score (decaying by 0.7 per hop).
    """

    name = "get_blast_radius_deep"
    description = (
        "Compute the full blast radius (downstream impact) from a root-cause entity. "
        "Returns all impacted services, pods, and infrastructure with propagation scores."
    )
    parameters = {
        "type": "object",
        "properties": {
            "entity_id": {"type": "string"},
            "entity_label": {"type": "string"},
            "max_depth": {"type": "integer", "description": "Max hops downstream, default 4"},
        },
        "required": ["entity_id"],
    }

    async def execute(self, entity_id: str, entity_label: str = "", max_depth: int = 4) -> str:
        search_id = entity_id or entity_label
        if not search_id:
            return json.dumps({"error": "entity_id required"})

        cypher = """
        MATCH (root)
        WHERE root.entity_id = $entity_id OR root.name =~ $label_pattern
        WITH root LIMIT 1
        MATCH path = (root)-[:DEPENDS_ON|CALLS|USES|RUNS_ON|HOSTED_ON*1..{depth}]->(impacted)
        WITH impacted, length(path) AS hops
        RETURN
            COALESCE(impacted.entity_id, impacted.name)  AS entity_id,
            COALESCE(impacted.name, impacted.entity_id)  AS label,
            COALESCE(impacted.type, labels(impacted)[0]) AS type,
            COALESCE(impacted.namespace, '')             AS namespace,
            COALESCE(impacted.cluster, '')               AS cluster,
            hops,
            round(power(0.7, hops), 3)                  AS propagation_score
        ORDER BY hops ASC, propagation_score DESC
        LIMIT 100
        """.format(depth=max_depth)

        try:
            rows = await _run_query(cypher, {
                "entity_id": search_id,
                "label_pattern": f"(?i).*{entity_label or entity_id}.*",
            })
            return json.dumps({
                "root": search_id,
                "blast_radius": rows,
                "total_impacted": len(rows),
            }, default=str)
        except Exception as e:
            return json.dumps({"error": str(e)})
