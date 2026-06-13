from __future__ import annotations
import os
import json
import asyncio
import aiohttp
from .base import BaseTool

DT_URL = os.getenv("DYNATRACE_URL", "https://mps-dynatrace-hybrid.k.miao.example.com")
DT_TOKEN = os.getenv("DYNATRACE_TOKEN", "")

_PLACEHOLDER = "dynatrace_api_token_placeholder"


def _headers() -> dict:
    return {"Authorization": f"Api-Token {DT_TOKEN}", "Content-Type": "application/json"}


def _token_ok() -> bool:
    return bool(DT_TOKEN) and DT_TOKEN != _PLACEHOLDER


async def _dt_get(session: aiohttp.ClientSession, path: str, params: dict) -> dict:
    """GET a Dynatrace API endpoint and return parsed JSON, or raise with a clear message."""
    async with session.get(
        f"{DT_URL}{path}", headers=_headers(), params=params,
        ssl=False, timeout=aiohttp.ClientTimeout(total=15)
    ) as resp:
        text = await resp.text()
        if resp.status == 401:
            raise PermissionError(f"Dynatrace token invalid or expired (HTTP 401). Configure DYNATRACE_TOKEN.")
        if resp.status == 403:
            raise PermissionError(f"Dynatrace token lacks required permission for {path} (HTTP 403).")
        if resp.status >= 400:
            raise RuntimeError(f"Dynatrace API error {resp.status} for {path}: {text[:200]}")
        try:
            return json.loads(text)
        except Exception:
            raise RuntimeError(f"Dynatrace returned non-JSON response for {path}: {text[:200]}")


class GetDynatraceProblems(BaseTool):
    name = "get_dynatrace_problems"
    description = "Get active or recent problems from Dynatrace. Returns problem title, impact, root cause entity, and affected services."
    parameters = {
        "type": "object",
        "properties": {
            "status": {"type": "string", "description": "OPEN or CLOSED, default OPEN"},
            "entity_selector": {"type": "string", "description": "Filter by entity e.g. 'type(SERVICE),tag(env:prod)'"},
            "limit": {"type": "integer", "description": "Max results, default 20"},
            "from": {"type": "string", "description": "Time range e.g. 'now-2h', default 'now-1h'"},
        },
        "required": [],
    }

    async def execute(self, status: str = "OPEN", entity_selector: str = "",
                      limit: int = 20, **kwargs) -> str:
        if not _token_ok():
            return json.dumps({"error": "Dynatrace not configured (placeholder token). Check DYNATRACE_TOKEN env var.", "problems": []})
        params = {
            "problemSelector": f"status({status})",
            "pageSize": limit,
            "fields": "+impactedEntities,+recentComments,+rootCauseEntity",
            "from": kwargs.get("from", "now-1h"),
        }
        if entity_selector:
            params["entitySelector"] = entity_selector
        async with aiohttp.ClientSession() as session:
            data = await _dt_get(session, "/api/v2/problems", params)
        problems = data.get("problems", [])
        results = []
        for p in problems:
            results.append({
                "id": p.get("problemId"),
                "title": p.get("title"),
                "status": p.get("status"),
                "severity": p.get("severityLevel"),
                "impact": p.get("impactLevel"),
                "started": p.get("startTime"),
                "root_cause": p.get("rootCauseEntity", {}).get("name") if p.get("rootCauseEntity") else None,
                "impacted": [e.get("name") for e in p.get("impactedEntities", [])[:5]],
            })
        return json.dumps(results, default=str)


class GetDynatraceMetrics(BaseTool):
    name = "get_dynatrace_metrics"
    description = "Query Dynatrace metrics for a service or host. Use to check CPU, memory, error rate, response time, throughput."
    parameters = {
        "type": "object",
        "properties": {
            "metric_selector": {"type": "string", "description": "e.g. 'builtin:service.errors.total.rate' or 'builtin:host.cpu.usage'"},
            "entity_selector": {"type": "string", "description": "e.g. 'type(SERVICE),entityName(my-service)'"},
            "from": {"type": "string", "description": "e.g. 'now-1h'"},
            "resolution": {"type": "string", "description": "e.g. '1m', '5m'"},
        },
        "required": ["metric_selector"],
    }

    async def execute(self, metric_selector: str, entity_selector: str = "",
                      from_time: str = "now-1h", resolution: str = "5m", **kwargs) -> str:
        if not _token_ok():
            return json.dumps({"error": "Dynatrace not configured (placeholder token).", "result": []})
        params = {
            "metricSelector": metric_selector,
            "from": kwargs.get("from", from_time),
            "resolution": resolution,
        }
        if entity_selector:
            params["entitySelector"] = entity_selector
        async with aiohttp.ClientSession() as session:
            data = await _dt_get(session, "/api/v2/metrics/query", params)
        result = data.get("result", [])
        if not result:
            return "No metric data found"
        out = []
        for r in result[:3]:
            entity_id = r.get("dimensionMap", {})
            data_points = r.get("data", [{}])[0]
            values = data_points.get("values", [])
            timestamps = data_points.get("timestamps", [])
            recent = list(zip(timestamps[-10:], values[-10:])) if values else []
            out.append({"metric": r.get("metricId"), "entity": entity_id, "recent_values": recent})
        return json.dumps(out, default=str)


class GetDynatraceEvents(BaseTool):
    name = "get_dynatrace_events"
    description = "Get deployment events, config changes, and anomaly events from Dynatrace. Good for correlating incidents with recent deployments."
    parameters = {
        "type": "object",
        "properties": {
            "event_type": {"type": "string", "description": "e.g. 'DEPLOYMENT' or 'CUSTOM_ANNOTATION' or empty for all"},
            "entity_selector": {"type": "string"},
            "from": {"type": "string", "description": "e.g. 'now-2h'"},
        },
        "required": [],
    }

    async def execute(self, event_type: str = "", entity_selector: str = "", **kwargs) -> str:
        if not _token_ok():
            return json.dumps({"error": "Dynatrace not configured (placeholder token).", "events": []})
        params = {"from": kwargs.get("from", "now-2h"), "pageSize": 30}
        if event_type:
            params["eventSelector"] = f"eventType({event_type})"
        if entity_selector:
            params["entitySelector"] = entity_selector
        async with aiohttp.ClientSession() as session:
            data = await _dt_get(session, "/api/v2/events", params)
        events = data.get("events", [])
        results = [
            {
                "type": e.get("eventType"),
                "title": e.get("title"),
                "start": e.get("startTime"),
                "entity": e.get("entityId", {}).get("name"),
                "properties": {k: v for k, v in list(e.get("properties", {}).items())[:5]},
            }
            for e in events
        ]
        return json.dumps(results, default=str)


class GetDynatraceTraces(BaseTool):
    name = "get_dynatrace_traces"
    description = "Get distributed traces for a service to find slow spans, errors, and dependency bottlenecks."
    parameters = {
        "type": "object",
        "properties": {
            "service_name": {"type": "string", "description": "Service name to query traces for"},
            "from": {"type": "string", "description": "e.g. 'now-30m'"},
            "error_only": {"type": "boolean", "description": "Only return errored traces"},
        },
        "required": ["service_name"],
    }

    async def execute(self, service_name: str, error_only: bool = True, **kwargs) -> str:
        if not _token_ok():
            return json.dumps({"error": "Dynatrace not configured (placeholder token).", "traces": []})
        query = f"service.name = \"{service_name}\""
        if error_only:
            query += " AND status = error"
        body = {
            "query": query,
            "startTime": kwargs.get("from", "now-30m"),
            "endTime": "now",
            "limit": 20,
        }
        async with aiohttp.ClientSession() as session:
            async with session.post(
                f"{DT_URL}/api/v2/traces", headers=_headers(), json=body,
                ssl=False, timeout=aiohttp.ClientTimeout(total=20)
            ) as resp:
                if resp.status == 404:
                    return "Traces API not available on this Dynatrace instance"
                if resp.status == 401:
                    return "Dynatrace token invalid (401)"
                data = await resp.json()
        traces = data.get("traces", [])
        results = [
            {
                "trace_id": t.get("traceId"),
                "duration_ms": t.get("duration"),
                "status": t.get("status"),
                "root_service": t.get("rootServiceName"),
                "error_count": t.get("errorCount", 0),
            }
            for t in traces[:10]
        ]
        return json.dumps(results, default=str)
