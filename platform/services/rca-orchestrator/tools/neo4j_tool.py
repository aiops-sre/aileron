from __future__ import annotations
import os
import json
from neo4j import AsyncGraphDatabase
from .base import BaseTool

NEO4J_URL = os.getenv("NEO4J_URL", "bolt://neo4j.alert-engine-poc.svc.cluster.local:7687")

def _neo4j_auth() -> tuple[str, str]:
    """Parse credentials: prefer NEO4J_AUTH (user/pass format), fall back to separate vars."""
    auth_str = os.getenv("NEO4J_AUTH", "")
    if auth_str and "/" in auth_str:
        user, _, password = auth_str.partition("/")
        return user, password
    return (
        os.getenv("NEO4J_USER", "neo4j"),
        os.getenv("NEO4J_PASSWORD", ""),
    )


async def _run_query(cypher: str, params: dict = {}) -> list[dict]:
    user, password = _neo4j_auth()
    async with AsyncGraphDatabase.driver(NEO4J_URL, auth=(user, password)) as driver:
        async with driver.session() as session:
            result = await session.run(cypher, **params)
            return [dict(record) async for record in result]


class GetTopologyTool(BaseTool):
    name = "get_topology"
    description = "Get service topology and dependencies from Neo4j. Use to understand what services depend on a failing component."
    parameters = {
        "type": "object",
        "properties": {
            "service_name": {"type": "string", "description": "Service or component name"},
            "depth": {"type": "integer", "description": "Relationship depth, default 2"},
        },
        "required": ["service_name"],
    }

    async def execute(self, service_name: str, depth: int = 2) -> str:
        cypher = """
        MATCH path = (n)-[*1..{depth}]-(m)
        WHERE n.name =~ $name_pattern OR n.service =~ $name_pattern
        RETURN n.name AS source, type(last(relationships(path))) AS rel,
               m.name AS target, m.type AS target_type, m.namespace AS namespace
        LIMIT 50
        """.format(depth=depth)
        results = await _run_query(cypher, {"name_pattern": f"(?i).*{service_name}.*"})
        return json.dumps(results, default=str)


class GetBlastRadiusTool(BaseTool):
    name = "get_blast_radius"
    description = "Get blast radius of a failing component — what services and users are impacted if this component goes down."
    parameters = {
        "type": "object",
        "properties": {
            "component": {"type": "string", "description": "The failing component name"},
            "namespace": {"type": "string"},
        },
        "required": ["component"],
    }

    async def execute(self, component: str, namespace: str = "") -> str:
        where = "WHERE n.name =~ $pattern"
        if namespace:
            where += " AND n.namespace = $namespace"
        cypher = f"""
        MATCH (n) {where}
        CALL apoc.path.subgraphAll(n, {{maxLevel: 3, relationshipFilter: 'DEPENDS_ON>|CALLS>|USES>'}})
        YIELD nodes, relationships
        RETURN [node in nodes | {{name: node.name, type: node.type, namespace: node.namespace}}] AS impacted
        LIMIT 1
        """
        try:
            results = await _run_query(cypher, {"pattern": f"(?i).*{component}.*", "namespace": namespace})
            impacted = results[0]["impacted"] if results else []
        except Exception:
            # Fallback without APOC
            cypher2 = """
            MATCH (n)-[:DEPENDS_ON|CALLS|USES*1..3]->(m)
            WHERE n.name =~ $pattern
            RETURN DISTINCT m.name AS name, m.type AS type, m.namespace AS namespace
            LIMIT 30
            """
            results = await _run_query(cypher2, {"pattern": f"(?i).*{component}.*"})
            impacted = results
        return json.dumps({"component": component, "downstream_impact": impacted}, default=str)


class GetRecentChangesTool(BaseTool):
    name = "get_recent_changes"
    description = "Get recent infrastructure changes (deployments, config changes, scaling events) stored in topology graph."
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string"},
            "hours_back": {"type": "integer", "description": "How many hours to look back, default 24"},
        },
        "required": [],
    }

    async def execute(self, namespace: str = "", hours_back: int = 24) -> str:
        where = "WHERE c.timestamp > datetime() - duration({hours: $hours})"
        if namespace:
            where += " AND c.namespace = $namespace"
        cypher = f"""
        MATCH (c:Change) {where}
        RETURN c.type AS type, c.service AS service, c.namespace AS namespace,
               c.timestamp AS timestamp, c.description AS description, c.author AS author
        ORDER BY c.timestamp DESC LIMIT 30
        """
        try:
            results = await _run_query(cypher, {"hours": hours_back, "namespace": namespace})
        except Exception:
            results = []
        return json.dumps(results, default=str)
