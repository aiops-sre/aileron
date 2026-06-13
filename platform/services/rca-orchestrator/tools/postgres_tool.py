from __future__ import annotations
import os
import json
import asyncio
import asyncpg
from .base import BaseTool

PG_DSN = os.getenv("POSTGRES_URL", "postgresql://alerthub:@postgres-primary.aileron.svc.cluster.local:5432/alerthub")


async def _query(sql: str, *args) -> list[dict]:
    conn = await asyncpg.connect(PG_DSN, timeout=10)
    try:
        rows = await conn.fetch(sql, *args)
        return [dict(r) for r in rows]
    finally:
        await conn.close()


class GetHistoricalAlertsTool(BaseTool):
    name = "get_historical_alerts"
    description = "Get historical alerts from the database. Use to find recurring patterns, previous occurrences of this type of issue."
    parameters = {
        "type": "object",
        "properties": {
            "service": {"type": "string", "description": "Service name filter"},
            "alert_type": {"type": "string", "description": "Alert type or title keyword"},
            "hours_back": {"type": "integer", "description": "Hours to look back, default 72"},
            "limit": {"type": "integer", "description": "Max results, default 20"},
        },
        "required": [],
    }

    async def execute(self, service: str = "", alert_type: str = "", hours_back: int = 72, limit: int = 20) -> str:
        args: list = [int(hours_back)]
        conditions = ["created_at > NOW() - $1 * INTERVAL '1 hour'"]
        if service:
            args.append(f"%{service}%")
            p = len(args)
            conditions.append(f"(source ILIKE ${p} OR labels::text ILIKE ${p})")
        if alert_type:
            args.append(f"%{alert_type}%")
            conditions.append(f"title ILIKE ${len(args)}")
        where = " AND ".join(conditions)
        sql = f"""
            SELECT id, title, severity, source, status, created_at, resolved_at,
                   EXTRACT(EPOCH FROM (resolved_at - created_at))/60 AS duration_min
            FROM alerts WHERE {where}
            ORDER BY created_at DESC LIMIT {int(limit)}
        """
        try:
            rows = await _query(sql, *args)
        except Exception as e:
            return f"Query failed: {e}"
        return json.dumps(rows, default=str)


class GetAlertFrequencyTool(BaseTool):
    name = "get_alert_frequency"
    description = "Get alert frequency trend — how often this type of alert fires. Use to detect if a new spike indicates something unusual."
    parameters = {
        "type": "object",
        "properties": {
            "title_keyword": {"type": "string", "description": "Keyword in alert title"},
            "bucket": {"type": "string", "description": "Time bucket: hour or day, default hour"},
            "days_back": {"type": "integer", "description": "Days to analyze, default 7"},
        },
        "required": ["title_keyword"],
    }

    async def execute(self, title_keyword: str, bucket: str = "hour", days_back: int = 7) -> str:
        allowed_buckets = {"hour", "day", "week", "month"}
        bucket = bucket if bucket in allowed_buckets else "hour"
        sql = f"""
            SELECT DATE_TRUNC('{bucket}', created_at) AS period, COUNT(*) AS count
            FROM alerts
            WHERE title ILIKE $1 AND created_at > NOW() - $2 * INTERVAL '1 day'
            GROUP BY 1 ORDER BY 1 DESC LIMIT 48
        """
        try:
            rows = await _query(sql, f"%{title_keyword}%", int(days_back))
        except Exception as e:
            return f"Query failed: {e}"
        return json.dumps(rows, default=str)


class GetResolvedIncidentsTool(BaseTool):
    name = "get_resolved_incidents"
    description = "Get recently resolved incidents with their resolution details. Use to find how similar past issues were fixed."
    parameters = {
        "type": "object",
        "properties": {
            "keyword": {"type": "string", "description": "Search in title or description"},
            "limit": {"type": "integer", "description": "Max results, default 10"},
        },
        "required": [],
    }

    async def execute(self, keyword: str = "", limit: int = 10) -> str:
        args: list = []
        conditions = ["status = 'resolved'"]
        if keyword:
            args.append(f"%{keyword}%")
            p = len(args)
            conditions.append(f"(title ILIKE ${p} OR description ILIKE ${p})")
        where = " AND ".join(conditions)
        sql = f"""
            SELECT id, title, severity, source, created_at, resolved_at, description,
                   EXTRACT(EPOCH FROM (resolved_at - created_at))/60 AS duration_min
            FROM incidents WHERE {where}
            ORDER BY resolved_at DESC LIMIT {int(limit)}
        """
        try:
            rows = await _query(sql, *args)
        except Exception as e:
            return f"Query failed: {e}"
        return json.dumps(rows, default=str)
