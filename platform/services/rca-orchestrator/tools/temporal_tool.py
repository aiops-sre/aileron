from __future__ import annotations
import json
import logging
import os
import time
from typing import Any

import asyncpg

from .base import BaseTool

log = logging.getLogger(__name__)

PG_DSN = os.getenv(
    "POSTGRES_URL",
    "postgresql://alerthub:@postgres-primary.alert-engine-poc.svc.cluster.local:5432/alerthub",
)


class QueryTemporalPropagationTool(BaseTool):
    """Reconstruct the temporal alert sequence within an incident's blast radius.

    Returns alerts ordered by first_fired_at with their infra_level so the
    causal graph can assign temporal_score (first-fired = more likely root cause).
    """

    name = "query_temporal_propagation"
    description = (
        "Retrieve the temporal alert propagation sequence for an incident. "
        "Returns alerts sorted by first-fired time with entity and infra level metadata."
    )
    parameters = {
        "type": "object",
        "properties": {
            "incident_id": {
                "type": "string",
                "description": "The UUID of the incident to reconstruct",
            },
            "namespace": {
                "type": "string",
                "description": "Optional Kubernetes namespace filter",
            },
        },
        "required": ["incident_id"],
    }

    async def execute(self, incident_id: str, namespace: str = "") -> str:
        t0 = time.monotonic()
        if not incident_id:
            return json.dumps({"error": "incident_id required"})
        try:
            conn = await asyncpg.connect(PG_DSN, timeout=10)
            rows = await conn.fetch(
                """
                SELECT
                    a.id::text                                          AS alert_id,
                    a.title                                             AS alert_title,
                    a.source                                            AS source,
                    a.labels->>'entity_id'                             AS entity_id,
                    a.labels->>'infra_level'                           AS infra_level,
                    a.labels->>'namespace'                             AS namespace,
                    a.labels->>'cluster'                               AS cluster,
                    a.labels->>'service'                               AS service,
                    COALESCE(a.first_fired_at, a.created_at)           AS fired_at,
                    EXTRACT(EPOCH FROM (
                        COALESCE(a.first_fired_at, a.created_at) -
                        MIN(COALESCE(a.first_fired_at, a.created_at)) OVER ()
                    ))::int                                             AS seconds_after_first
                FROM alerts a
                WHERE a.incident_id = $1::uuid
                  AND ($2 = '' OR a.labels->>'namespace' = $2)
                ORDER BY fired_at ASC
                LIMIT 100
                """,
                incident_id,
                namespace,
            )
            await conn.close()
            result = [dict(r) for r in rows]
            log.info(f"Temporal propagation: {len(result)} alerts for incident {incident_id}")
            return json.dumps(result, default=str)
        except Exception as e:
            log.warning(f"Temporal propagation query failed: {e}")
            return json.dumps({"error": str(e)})
